package app

import (
	"context"
	"errors"
	"sort"
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
	replay    eventReplay
	changed   chan struct{}
	finished  chan struct{}
	done      bool
	result    Result
	err       error
	started   time.Time
	completed time.Time
	nextSeq   int64
	approvals map[string]pendingApproval
}

func newRun(sessionID string, cancel context.CancelFunc, maxEvents int, started time.Time) *Run {
	return &Run{
		id:        newRuntimeID(),
		sessionID: sessionID,
		cancel:    cancel,
		changed:   make(chan struct{}),
		started:   started,
		replay:    eventReplay{maxEvents: maxEvents, maxBytes: defaultMaxEventBytes},
		finished:  make(chan struct{}),
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
		FirstSequence: r.replay.firstSequence(r.nextSeq),
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
		events, err := r.replay.after(afterSequence, r.nextSeq)
		if err != nil || len(events) > 0 {
			done := r.done
			r.mu.Unlock()
			return events, done, err
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
	defer r.mu.Unlock()
	r.publishLocked(event)
	r.notifyLocked()
}

func (r *Run) publishLocked(event Event) {
	r.nextSeq++
	event.ID = newRuntimeID()
	event.SessionID = r.sessionID
	event.RunID = r.id
	event.Sequence = r.nextSeq
	event.Timestamp = time.Now().UnixMilli()
	r.replay.append(event)
}

func (r *Run) notifyLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}

// complete keeps the execution outcome independently of the replay window.
// TreeItems belong to the session projection and are only sent to transports;
// retaining them here would retain a second copy of the full conversation.
func (r *Run) complete(result Result, err error, completed time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.result = Result{Content: result.Content, Usage: result.Usage, ModelName: result.ModelName}
	r.err = err
	event := Event{Type: EventRunCompleted, Result: &result}
	if errors.Is(err, context.Canceled) {
		event = Event{Type: EventRunCanceled, Error: err.Error()}
	} else if err != nil {
		code, retryable := runtimeErrorDetails(err)
		event = Event{Type: EventRunFailed, Error: err.Error(), ErrorCode: code, Retryable: retryable, Result: &result}
	}
	r.publishLocked(event)
	r.done = true
	r.completed = completed
	close(r.finished)
	r.notifyLocked()
}

// Await waits for the execution outcome, even when replay events have expired.
// The returned result contains content, usage and model selection. Session
// trees remain available through the session snapshot API.
func (r *Run) Await(ctx context.Context) (Result, error) {
	r.mu.Lock()
	if r.done {
		result, err := r.result, r.err
		r.mu.Unlock()
		return result, err
	}
	finished := r.finished
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-finished:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.result, r.err
	}
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
