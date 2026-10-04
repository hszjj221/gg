package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/runlog"
	"github.com/hszjj221/gg/internal/session"
)

// Manager owns many conversation services and their active runs. A session has
// at most one active run; separate sessions can execute concurrently.
type ManagerOptions struct {
	// RunTimeout bounds a complete turn, including provider calls and approvals.
	// Zero uses 30 minutes; negative disables the deadline.
	RunTimeout time.Duration
	// MaxActiveRuns bounds concurrent turns across a Runtime's workspaces.
	// Zero uses 16; negative disables the limit.
	MaxActiveRuns int
	// MaxEventBytes bounds the serialized replay data retained by each run.
	// Oversized events expire replay history; clients reload a snapshot.
	// Zero uses 4 MiB; negative disables the byte limit.
	MaxEventBytes int
	limiter       *runLimiter

	// CompletedRunTTL controls how long completed runs remain replayable.
	// Zero uses the default; a negative value disables age-based eviction.
	CompletedRunTTL time.Duration
	// MaxCompletedRuns bounds completed run metadata and replay buffers.
	// Zero uses the default; a negative value disables count-based eviction.
	MaxCompletedRuns int
	// MaxOpenSessions bounds in-memory conversation services. Inactive sessions
	// are evicted least-recently-used and transparently reopened by the Runtime.
	// Zero uses the default; a negative value disables count-based eviction.
	MaxOpenSessions int
	// MaxEventsPerRun bounds each replay buffer. Clients that fall behind the
	// retained window receive event_history_expired and can reload a snapshot.
	// Zero uses the default; a negative value disables the bound.
	MaxEventsPerRun int
	// Clock is primarily useful for deterministic lifecycle tests.
	Clock func() time.Time
	// Log receives run lifecycle diagnostics and panic reports. Nil disables
	// manager logging; the daemon sets it to its stderr logger. A panicking
	// run is always recorded as failed even when Log is nil.
	Log *slog.Logger
}

const (
	defaultCompletedRunTTL  = 30 * time.Minute
	defaultMaxCompletedRuns = 128
	defaultMaxOpenSessions  = 64
	defaultMaxEventsPerRun  = 8192
	defaultMaxEventBytes    = 4 << 20
	defaultMaxActiveRuns    = 16
	defaultRunTimeout       = 30 * time.Minute
)

type runLimiter struct {
	mu            sync.Mutex
	active, limit int
}

func (l *runLimiter) acquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit >= 0 && l.active >= l.limit {
		return false
	}
	l.active++
	return true
}
func (l *runLimiter) release() { l.mu.Lock(); l.active--; l.mu.Unlock() }
func newRunLimiter(limit int) *runLimiter {
	if limit == 0 {
		limit = defaultMaxActiveRuns
	}
	return &runLimiter{limit: limit}
}

type Manager struct {
	closed    bool
	workers   sync.WaitGroup
	closeDone chan struct{}
	closeErr  error

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
	if options.RunTimeout == 0 {
		options.RunTimeout = defaultRunTimeout
	}
	if options.MaxEventBytes == 0 {
		options.MaxEventBytes = defaultMaxEventBytes
	}
	if options.limiter == nil {
		options.limiter = newRunLimiter(options.MaxActiveRuns)
	}

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
		closeDone:     make(chan struct{}),
	}
}

func (m *Manager) Add(service *Service) (string, error) {
	canonical, err := m.getOrAdd(service)
	if err != nil {
		return "", err
	}
	return canonical.Snapshot().SessionID, nil
}

func (m *Manager) getOrAdd(service *Service) (*Service, error) {
	if service == nil {
		return nil, fmt.Errorf("conversation service is required")
	}
	id := service.Snapshot().SessionID
	if id == "" {
		return nil, fmt.Errorf("conversation must have a persisted session")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = service.Close()
		return nil, errorf(ErrorRuntimeClosed, false, "runtime is closed")
	}
	if existing := m.sessions[id]; existing != nil {
		m.touchSessionLocked(id)
		m.mu.Unlock()
		if existing != service {
			_ = service.Close()
		}
		return existing, nil
	}
	m.sessions[id] = service
	m.touchSessionLocked(id)
	evicted := m.pruneSessionsLocked(id)
	m.mu.Unlock()
	closeServices(evicted)
	return service, nil
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
	return m.startTurn(parent, sessionID, prompt, func(run *Run) agent.Approver {
		if requireApproval {
			return runApprover{run: run}
		}
		return nil
	})
}

// StartTurnWithApprover starts a turn with an explicit approver instead of the
// interactive one. It exists for unattended execution (e.g. scheduled jobs)
// where no human is available to answer approval prompts; callers pass a
// policy approver such as scheduler.UnattendedApprover.
func (m *Manager) StartTurnWithApprover(parent context.Context, sessionID, prompt string, approver agent.Approver) (*Run, error) {
	return m.startTurn(parent, sessionID, prompt, func(*Run) agent.Approver { return approver })
}

func (m *Manager) startTurn(parent context.Context, sessionID, prompt string, approverFor func(*Run) agent.Approver) (*Run, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errorf(ErrorRuntimeClosed, false, "runtime is closed")
	}
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
	if !m.options.limiter.acquire() {
		m.mu.Unlock()
		return nil, errorf(ErrorRunCapacity, true, "active run capacity reached; retry later")
	}
	ctx, cancel := context.WithCancel(parent)
	if m.options.RunTimeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(parent, m.options.RunTimeout)
	}
	m.workers.Add(1)
	run := newRun(sessionID, cancel, m.options.MaxEventsPerRun, m.options.Clock())
	run.replay.maxBytes = m.options.MaxEventBytes
	m.runs[run.id] = run
	m.active[sessionID] = run.id
	m.touchSessionLocked(sessionID)
	m.mu.Unlock()

	ctx = context.WithValue(ctx, runIDKey{}, run.id)
	if m.options.Log != nil {
		m.options.Log.InfoContext(ctx, "run started", "sessionID", sessionID, "runID", run.id)
	}
	run.publish(Event{Type: EventRunStarted})
	go func() {
		defer m.workers.Done()
		releaseCapacity := sync.OnceFunc(m.options.limiter.release)
		defer releaseCapacity()
		approver := approverFor(run)
		// A panic in the agent run must not kill the host process (the
		// daemon serves many turns): recover it and let the normal
		// completion path below record the run as failed.
		result, err := func() (result Result, err error) {
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					if m.options.Log != nil {
						m.options.Log.Error("agent run panicked",
							"sessionID", sessionID, "runID", run.id,
							"panic", fmt.Sprintf("%v", r), "stack", string(stack))
					}
					err = fmt.Errorf("panic: %v (see daemon log for stack trace)", r)
				}
			}()
			return service.Run(ctx, prompt, func(agentEvent agent.Event) {
				event := agentEvent
				run.publish(Event{Type: EventAgent, Agent: &event})
			}, approver)
		}()
		cancel()
		completed := m.options.Clock()
		releaseCapacity()
		// Emit terminal diagnostics before exposing completion to waiters,
		// outside the manager lock so log I/O cannot block other sessions.
		if m.options.Log != nil {
			level := slog.LevelInfo
			if err != nil && !errors.Is(err, context.Canceled) {
				level = slog.LevelWarn
			}
			code, retryable := runtimeErrorDetails(err)
			m.options.Log.Log(ctx, level, "run finished", "sessionID", sessionID, "runID", run.id, "model", result.ModelName, "durationMs", float64(completed.Sub(run.started).Microseconds())/1000, "outcome", runlog.Outcome(err), "errorCode", code, "retryable", retryable, "promptTokens", result.Usage.PromptTokens, "completionTokens", result.Usage.CompletionTokens, "totalTokens", result.Usage.TotalTokens)
		}
		m.mu.Lock()
		if m.active[sessionID] == run.id {
			delete(m.active, sessionID)
		}
		var evicted []*Service
		if errors.Is(err, session.ErrConflict) && m.sessions[sessionID] == service {
			delete(m.sessions, sessionID)
			delete(m.sessionAccess, sessionID)
			evicted = append(evicted, service)
		}
		run.complete(result, err, completed)
		m.pruneRunsLocked(completed)
		evicted = append(evicted, m.pruneSessionsLocked("")...)
		m.mu.Unlock()
		closeServices(evicted)
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
	if m.active[sessionID] != "" {
		m.mu.Unlock()
		return false
	}
	service, ok := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	delete(m.sessionAccess, sessionID)
	m.mu.Unlock()
	if service != nil {
		_ = service.Close()
	}
	return ok
}

// Close stops admission, cancels all turns, then releases resources after
// workers exit. A caller deadline limits waiting without abandoning cleanup.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		runs := make([]*Run, 0, len(m.active))
		for _, id := range m.active {
			if run := m.runs[id]; run != nil {
				runs = append(runs, run)
			}
		}
		go func() {
			for _, run := range runs {
				run.Cancel()
			}
			m.workers.Wait()
			m.mu.Lock()
			services := make([]*Service, 0, len(m.sessions))
			for _, service := range m.sessions {
				services = append(services, service)
			}
			clear(m.sessions)
			clear(m.sessionAccess)
			m.mu.Unlock()
			var errs []error
			for _, service := range services {
				errs = append(errs, service.Close())
			}
			m.closeErr = errors.Join(errs...)
			close(m.closeDone)
		}()
	}
	done := m.closeDone
	m.mu.Unlock()
	select {
	case <-done:
		return m.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closeServices(services []*Service) {
	for _, service := range services {
		_ = service.Close()
	}
}

func (m *Manager) touchSessionLocked(sessionID string) {
	m.accessSeq++
	m.sessionAccess[sessionID] = m.accessSeq
}

func (m *Manager) pruneSessionsLocked(protected string) []*Service {
	var evicted []*Service
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
			return evicted
		}
		service := m.sessions[candidate]
		delete(m.sessions, candidate)
		delete(m.sessionAccess, candidate)
		evicted = append(evicted, service)
	}
	return evicted
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
