package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
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
}

type Approval struct {
	ID      string                `json:"id"`
	Request agent.ApprovalRequest `json:"request"`
}

type Run struct {
	id        string
	sessionID string
	cancel    context.CancelFunc

	mu        sync.Mutex
	events    []Event
	changed   chan struct{}
	done      bool
	approvals map[string]chan agent.ApprovalDecision
}

func newRun(sessionID string, cancel context.CancelFunc) *Run {
	return &Run{
		id:        newRuntimeID(),
		sessionID: sessionID,
		cancel:    cancel,
		changed:   make(chan struct{}),
		approvals: make(map[string]chan agent.ApprovalDecision),
	}
}

func (r *Run) ID() string { return r.id }

func (r *Run) SessionID() string { return r.sessionID }

func (r *Run) Cancel() { r.cancel() }

// Wait returns every event after afterSequence. It blocks until an event is
// available, the run finishes, or ctx is canceled. Retained events make this
// suitable for reconnecting transports.
func (r *Run) Wait(ctx context.Context, afterSequence int64) ([]Event, bool, error) {
	for {
		r.mu.Lock()
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
	event.ID = newRuntimeID()
	event.SessionID = r.sessionID
	event.RunID = r.id
	event.Sequence = int64(len(r.events) + 1)
	event.Timestamp = time.Now().UnixMilli()
	r.events = append(r.events, event)
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
}

func (r *Run) finish(event Event) {
	r.publish(event)
	r.mu.Lock()
	r.done = true
	close(r.changed)
	r.mu.Unlock()
}

func (r *Run) requestApproval(ctx context.Context, request agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	approval := Approval{ID: newRuntimeID(), Request: request}
	response := make(chan agent.ApprovalDecision, 1)
	r.mu.Lock()
	r.approvals[approval.ID] = response
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
	response, ok := r.approvals[approvalID]
	if ok {
		delete(r.approvals, approvalID)
	}
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("approval %q is not pending", approvalID)
	}
	response <- decision
	return nil
}

type runApprover struct{ run *Run }

func (a runApprover) Approve(ctx context.Context, request agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return a.run.requestApproval(ctx, request)
}

// Manager owns many conversation services and their active runs. A session has
// at most one active run; separate sessions can execute concurrently.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Service
	runs     map[string]*Run
	active   map[string]string
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Service),
		runs:     make(map[string]*Run),
		active:   make(map[string]string),
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
	return snapshot.SessionID, nil
}

func (m *Manager) Get(sessionID string) (*Service, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	service, ok := m.sessions[sessionID]
	return service, ok
}

func (m *Manager) Snapshots() []Snapshot {
	m.mu.RLock()
	services := make([]*Service, 0, len(m.sessions))
	for _, service := range m.sessions {
		services = append(services, service)
	}
	m.mu.RUnlock()
	result := make([]Snapshot, 0, len(services))
	for _, service := range services {
		result = append(result, service.Snapshot())
	}
	return result
}

func (m *Manager) StartTurn(parent context.Context, sessionID, prompt string, requireApproval bool) (*Run, error) {
	m.mu.Lock()
	service, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %q is not open", sessionID)
	}
	if runID := m.active[sessionID]; runID != "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %q already has active run %q", sessionID, runID)
	}
	ctx, cancel := context.WithCancel(parent)
	run := newRun(sessionID, cancel)
	m.runs[run.id] = run
	m.active[sessionID] = run.id
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
		m.mu.Lock()
		if m.active[sessionID] == run.id {
			delete(m.active, sessionID)
		}
		m.mu.Unlock()
		if err == nil {
			run.finish(Event{Type: EventRunCompleted, Result: &result})
		} else if errors.Is(err, context.Canceled) {
			run.finish(Event{Type: EventRunCanceled, Error: err.Error()})
		} else {
			run.finish(Event{Type: EventRunFailed, Error: err.Error(), Result: &result})
		}
	}()
	return run, nil
}

func (m *Manager) Run(runID string) (*Run, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[runID]
	return run, ok
}

func (m *Manager) Cancel(runID string) error {
	run, ok := m.Run(runID)
	if !ok {
		return fmt.Errorf("run %q not found", runID)
	}
	run.Cancel()
	return nil
}

func (m *Manager) Approve(runID, approvalID string, decision agent.ApprovalDecision) error {
	run, ok := m.Run(runID)
	if !ok {
		return fmt.Errorf("run %q not found", runID)
	}
	return run.approve(approvalID, decision)
}

func (m *Manager) Steer(sessionID, text string, followUp bool) error {
	m.mu.RLock()
	service, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session %q is not open", sessionID)
	}
	service.Queue().Add(text, followUp)
	return nil
}

func newRuntimeID() string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err == nil {
		return hex.EncodeToString(data[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
