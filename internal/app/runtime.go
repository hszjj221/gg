package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/session"
)

type EventType string

const (
	EventRunStarted        EventType = "run_started"
	EventAgent             EventType = "agent_event"
	EventApprovalRequested EventType = "approval_requested"
	EventApprovalResolved  EventType = "approval_resolved"
	EventRunCompleted      EventType = "run_completed"
	EventRunFailed         EventType = "run_failed"
	EventRunCanceled       EventType = "run_canceled"
)

// Event is the stable envelope shared by stdio and network transports. Sequence
// is scoped to a run and allows clients to resume after their last seen event.
type Event struct {
	ID        string                  `json:"id"`
	SessionID string                  `json:"sessionId"`
	RunID     string                  `json:"runId"`
	Sequence  int64                   `json:"sequence"`
	Timestamp int64                   `json:"timestamp"`
	Type      EventType               `json:"type"`
	Agent     *agent.Event            `json:"agent,omitempty"`
	Approval  *Approval               `json:"approval,omitempty"`
	Decision  *agent.ApprovalDecision `json:"decision,omitempty"`
	Result    *Result                 `json:"result,omitempty"`
	Error     string                  `json:"error,omitempty"`
	ErrorCode ErrorCode               `json:"errorCode,omitempty"`
	Retryable bool                    `json:"retryable,omitempty"`
}

type Approval struct {
	ID      string                `json:"id"`
	Request agent.ApprovalRequest `json:"request"`
}

// RunStatus is a reconnect-safe snapshot of a run. FirstSequence and
// LastSequence describe the currently retained replay window.
type RunStatus struct {
	ID               string     `json:"id"`
	SessionID        string     `json:"sessionId"`
	Done             bool       `json:"done"`
	StartedAt        int64      `json:"startedAt"`
	CompletedAt      int64      `json:"completedAt,omitempty"`
	FirstSequence    int64      `json:"firstSequence"`
	LastSequence     int64      `json:"lastSequence"`
	PendingApprovals []Approval `json:"pendingApprovals"`
}

type pendingApproval struct {
	approval Approval
	response chan agent.ApprovalDecision
}

type Run struct {
	id        string
	sessionID string
	cancel    context.CancelFunc

	mu        sync.Mutex
	events    []Event
	changed   chan struct{}
	done      bool
	started   time.Time
	completed time.Time
	nextSeq   int64
	maxEvents int
	approvals map[string]pendingApproval
}

func newRun(sessionID string, cancel context.CancelFunc, maxEvents int, started time.Time) *Run {
	return &Run{
		id:        newRuntimeID(),
		sessionID: sessionID,
		cancel:    cancel,
		changed:   make(chan struct{}),
		started:   started,
		maxEvents: maxEvents,
		approvals: make(map[string]pendingApproval),
	}
}

func (r *Run) ID() string { return r.id }

func (r *Run) SessionID() string { return r.sessionID }

func (r *Run) Cancel() { r.cancel() }

func (r *Run) Status() RunStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := RunStatus{
		ID:            r.id,
		SessionID:     r.sessionID,
		Done:          r.done,
		StartedAt:     r.started.UnixMilli(),
		LastSequence:  r.nextSeq,
		FirstSequence: r.nextSeq + 1,
	}
	if len(r.events) > 0 {
		status.FirstSequence = r.events[0].Sequence
	}
	if !r.completed.IsZero() {
		status.CompletedAt = r.completed.UnixMilli()
	}
	status.PendingApprovals = make([]Approval, 0, len(r.approvals))
	for _, pending := range r.approvals {
		status.PendingApprovals = append(status.PendingApprovals, pending.approval)
	}
	sort.Slice(status.PendingApprovals, func(i, j int) bool {
		return status.PendingApprovals[i].ID < status.PendingApprovals[j].ID
	})
	return status
}

// Wait returns every event after afterSequence. It blocks until an event is
// available, the run finishes, or ctx is canceled. Retained events make this
// suitable for reconnecting transports.
func (r *Run) Wait(ctx context.Context, afterSequence int64) ([]Event, bool, error) {
	for {
		r.mu.Lock()
		if len(r.events) > 0 && afterSequence < r.events[0].Sequence-1 {
			oldest := r.events[0].Sequence
			r.mu.Unlock()
			return nil, false, errorf(ErrorEventHistoryExpired, true, "run event history before sequence %d has expired", oldest)
		}
		start := len(r.events)
		for i, event := range r.events {
			if event.Sequence > afterSequence {
				start = i
				break
			}
		}
		if start < len(r.events) {
			events := append([]Event(nil), r.events[start:]...)
			done := r.done
			r.mu.Unlock()
			return events, done, nil
		}
		if r.done {
			r.mu.Unlock()
			return nil, true, nil
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-changed:
		}
	}
}

func (r *Run) publish(event Event) {
	r.mu.Lock()
	r.nextSeq++
	event.ID = newRuntimeID()
	event.SessionID = r.sessionID
	event.RunID = r.id
	event.Sequence = r.nextSeq
	event.Timestamp = time.Now().UnixMilli()
	r.events = append(r.events, event)
	if r.maxEvents > 0 && len(r.events) > r.maxEvents {
		drop := len(r.events) - r.maxEvents
		copy(r.events, r.events[drop:])
		r.events = r.events[:r.maxEvents]
	}
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
}

func (r *Run) finish(event Event, completed time.Time) {
	r.publish(event)
	r.mu.Lock()
	r.done = true
	r.completed = completed
	close(r.changed)
	r.mu.Unlock()
}

func (r *Run) completion() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completed, r.done
}

func (r *Run) requestApproval(ctx context.Context, request agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	approval := Approval{ID: newRuntimeID(), Request: request}
	response := make(chan agent.ApprovalDecision, 1)
	r.mu.Lock()
	r.approvals[approval.ID] = pendingApproval{approval: approval, response: response}
	r.mu.Unlock()
	r.publish(Event{Type: EventApprovalRequested, Approval: &approval})

	select {
	case <-ctx.Done():
		r.mu.Lock()
		delete(r.approvals, approval.ID)
		r.mu.Unlock()
		return agent.ApprovalDecision{}, ctx.Err()
	case decision := <-response:
		r.publish(Event{Type: EventApprovalResolved, Approval: &approval, Decision: &decision})
		return decision, nil
	}
}

func (r *Run) approve(approvalID string, decision agent.ApprovalDecision) error {
	r.mu.Lock()
	pending, ok := r.approvals[approvalID]
	if ok {
		delete(r.approvals, approvalID)
	}
	r.mu.Unlock()
	if !ok {
		return errorf(ErrorApprovalExpired, false, "approval %q is not pending", approvalID)
	}
	pending.response <- decision
	return nil
}

type runApprover struct{ run *Run }

func (a runApprover) Approve(ctx context.Context, request agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return a.run.requestApproval(ctx, request)
}

// Manager owns many conversation services and their active runs. A session has
// at most one active run; separate sessions can execute concurrently.
type ManagerOptions struct {
	// CompletedRunTTL controls how long completed runs remain replayable.
	// Zero uses the default; a negative value disables age-based eviction.
	CompletedRunTTL time.Duration
	// MaxCompletedRuns bounds completed run metadata and replay buffers.
	// Zero uses the default; a negative value disables count-based eviction.
	MaxCompletedRuns int
	// MaxOpenSessions bounds in-memory conversation services. Inactive sessions
	// are evicted least-recently-used and transparently reopened by Workspace.
	// Zero uses the default; a negative value disables count-based eviction.
	MaxOpenSessions int
	// MaxEventsPerRun bounds each replay buffer. Clients that fall behind the
	// retained window receive event_history_expired and can reload a snapshot.
	// Zero uses the default; a negative value disables the bound.
	MaxEventsPerRun int
	// Clock is primarily useful for deterministic lifecycle tests.
	Clock func() time.Time
}

const (
	defaultCompletedRunTTL  = 30 * time.Minute
	defaultMaxCompletedRuns = 128
	defaultMaxOpenSessions  = 64
	defaultMaxEventsPerRun  = 8192
)

type Manager struct {
	mu            sync.RWMutex
	sessions      map[string]*Service
	sessionAccess map[string]uint64
	runs          map[string]*Run
	active        map[string]string
	accessSeq     uint64
	options       ManagerOptions
}

func NewManager() *Manager {
	return NewManagerWithOptions(ManagerOptions{})
}

func NewManagerWithOptions(options ManagerOptions) *Manager {
	if options.CompletedRunTTL == 0 {
		options.CompletedRunTTL = defaultCompletedRunTTL
	}
	if options.MaxCompletedRuns == 0 {
		options.MaxCompletedRuns = defaultMaxCompletedRuns
	}
	if options.MaxOpenSessions == 0 {
		options.MaxOpenSessions = defaultMaxOpenSessions
	}
	if options.MaxEventsPerRun == 0 {
		options.MaxEventsPerRun = defaultMaxEventsPerRun
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &Manager{
		sessions:      make(map[string]*Service),
		sessionAccess: make(map[string]uint64),
		runs:          make(map[string]*Run),
		active:        make(map[string]string),
		options:       options,
	}
}

func (m *Manager) Add(service *Service) (string, error) {
	if service == nil {
		return "", fmt.Errorf("conversation service is required")
	}
	snapshot := service.Snapshot()
	if snapshot.SessionID == "" {
		return "", fmt.Errorf("conversation must have a persisted session")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[snapshot.SessionID] = service
	m.touchSessionLocked(snapshot.SessionID)
	m.pruneSessionsLocked(snapshot.SessionID)
	return snapshot.SessionID, nil
}

func (m *Manager) Get(sessionID string) (*Service, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneRunsLocked(m.options.Clock())
	service, ok := m.sessions[sessionID]
	if ok {
		m.touchSessionLocked(sessionID)
	}
	return service, ok
}

func (m *Manager) Snapshots() []Snapshot {
	m.mu.Lock()
	m.pruneRunsLocked(m.options.Clock())
	services := make([]*Service, 0, len(m.sessions))
	for sessionID, service := range m.sessions {
		services = append(services, service)
		m.touchSessionLocked(sessionID)
	}
	m.mu.Unlock()
	result := make([]Snapshot, 0, len(services))
	for _, service := range services {
		result = append(result, service.Snapshot())
	}
	return result
}

func (m *Manager) StartTurn(parent context.Context, sessionID, prompt string, requireApproval bool) (*Run, error) {
	m.mu.Lock()
	m.pruneRunsLocked(m.options.Clock())
	service, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, errorf(ErrorSessionNotFound, false, "session %q is not open", sessionID)
	}
	if runID := m.active[sessionID]; runID != "" {
		m.mu.Unlock()
		return nil, errorf(ErrorRunConflict, true, "session %q already has active run %q", sessionID, runID)
	}
	ctx, cancel := context.WithCancel(parent)
	run := newRun(sessionID, cancel, m.options.MaxEventsPerRun, m.options.Clock())
	m.runs[run.id] = run
	m.active[sessionID] = run.id
	m.touchSessionLocked(sessionID)
	m.mu.Unlock()

	run.publish(Event{Type: EventRunStarted})
	go func() {
		var approver agent.Approver
		if requireApproval {
			approver = runApprover{run: run}
		}
		result, err := service.Run(ctx, prompt, func(agentEvent agent.Event) {
			event := agentEvent
			run.publish(Event{Type: EventAgent, Agent: &event})
		}, approver)
		cancel()
		completed := m.options.Clock()
		if err == nil {
			run.finish(Event{Type: EventRunCompleted, Result: &result}, completed)
		} else if errors.Is(err, context.Canceled) {
			run.finish(Event{Type: EventRunCanceled, Error: err.Error()}, completed)
		} else {
			code, retryable := runtimeErrorDetails(err)
			run.finish(Event{Type: EventRunFailed, Error: err.Error(), ErrorCode: code, Retryable: retryable, Result: &result}, completed)
		}
		m.mu.Lock()
		if m.active[sessionID] == run.id {
			delete(m.active, sessionID)
		}
		if errors.Is(err, session.ErrConflict) {
			delete(m.sessions, sessionID)
			delete(m.sessionAccess, sessionID)
		}
		m.pruneRunsLocked(completed)
		m.pruneSessionsLocked("")
		m.mu.Unlock()
	}()
	return run, nil
}

func (m *Manager) Run(runID string) (*Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneRunsLocked(m.options.Clock())
	run, ok := m.runs[runID]
	return run, ok
}

func (m *Manager) RunStatus(runID string) (RunStatus, bool) {
	run, ok := m.Run(runID)
	if !ok {
		return RunStatus{}, false
	}
	return run.Status(), true
}

func (m *Manager) ActiveRun(sessionID string) (RunStatus, bool) {
	m.mu.Lock()
	m.pruneRunsLocked(m.options.Clock())
	runID := m.active[sessionID]
	run := m.runs[runID]
	m.mu.Unlock()
	if run == nil {
		return RunStatus{}, false
	}
	return run.Status(), true
}

func (m *Manager) Cancel(runID string) error {
	run, ok := m.Run(runID)
	if !ok {
		return errorf(ErrorRunNotFound, false, "run %q not found", runID)
	}
	run.Cancel()
	return nil
}

func (m *Manager) Approve(runID, approvalID string, decision agent.ApprovalDecision) error {
	run, ok := m.Run(runID)
	if !ok {
		return errorf(ErrorRunNotFound, false, "run %q not found", runID)
	}
	return run.approve(approvalID, decision)
}

func (m *Manager) Steer(sessionID, text string, followUp bool) error {
	m.mu.Lock()
	service, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return errorf(ErrorSessionNotFound, false, "session %q is not open", sessionID)
	}
	m.touchSessionLocked(sessionID)
	m.mu.Unlock()
	service.Queue().Add(text, followUp)
	return nil
}

func (m *Manager) Remove(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[sessionID] != "" {
		return false
	}
	if _, ok := m.sessions[sessionID]; !ok {
		return false
	}
	delete(m.sessions, sessionID)
	delete(m.sessionAccess, sessionID)
	return true
}

func (m *Manager) touchSessionLocked(sessionID string) {
	m.accessSeq++
	m.sessionAccess[sessionID] = m.accessSeq
}

func (m *Manager) pruneSessionsLocked(protected string) {
	limit := m.options.MaxOpenSessions
	for limit >= 0 && len(m.sessions) > limit {
		var candidate string
		var oldest uint64
		for sessionID := range m.sessions {
			if sessionID == protected || m.active[sessionID] != "" {
				continue
			}
			access := m.sessionAccess[sessionID]
			if candidate == "" || access < oldest {
				candidate = sessionID
				oldest = access
			}
		}
		if candidate == "" {
			return
		}
		delete(m.sessions, candidate)
		delete(m.sessionAccess, candidate)
	}
}

func (m *Manager) pruneRunsLocked(now time.Time) {
	type completedRun struct {
		id   string
		time time.Time
	}
	completed := make([]completedRun, 0, len(m.runs))
	for runID, run := range m.runs {
		finishedAt, done := run.completion()
		if !done {
			continue
		}
		if m.options.CompletedRunTTL >= 0 && now.Sub(finishedAt) > m.options.CompletedRunTTL {
			delete(m.runs, runID)
			continue
		}
		completed = append(completed, completedRun{id: runID, time: finishedAt})
	}
	limit := m.options.MaxCompletedRuns
	if limit < 0 || len(completed) <= limit {
		return
	}
	sort.Slice(completed, func(i, j int) bool {
		if completed[i].time.Equal(completed[j].time) {
			return completed[i].id < completed[j].id
		}
		return completed[i].time.Before(completed[j].time)
	})
	for _, item := range completed[:len(completed)-limit] {
		delete(m.runs, item.id)
	}
}

func runtimeErrorDetails(err error) (ErrorCode, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code, appErr.Retryable
	}
	if errors.Is(err, session.ErrConflict) || errors.Is(err, session.ErrLocked) {
		return ErrorSessionConflict, true
	}
	return "", false
}

func newRuntimeID() string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err == nil {
		return hex.EncodeToString(data[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
