package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

type runtimeProvider struct {
	block     bool
	toolFirst bool
	calls     int
}

func (p *runtimeProvider) Complete(ctx context.Context, request agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	p.calls++
	if p.block {
		<-ctx.Done()
		return agent.AssistantMessage{}, ctx.Err()
	}
	if p.toolFirst && p.calls == 1 {
		return agent.AssistantMessage{
			Message:    agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "bash", Arguments: []byte(`{"command":"printf ok"}`)}}},
			StopReason: agent.StopReasonToolUse,
		}, nil
	}
	if onEvent != nil {
		onEvent(agent.Event{Type: agent.EventTextDelta, Text: "done"})
	}
	return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "done"}, StopReason: agent.StopReasonEndTurn}, nil
}

func TestManagerPublishesReplayableRunEvents(t *testing.T) {
	manager, sessionID := runtimeTestManager(t, &runtimeProvider{})
	run, err := manager.StartTurn(context.Background(), sessionID, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	events := waitForRun(t, run, nil)
	if len(events) < 3 || events[0].Type != EventRunStarted || events[len(events)-1].Type != EventRunCompleted {
		t.Fatalf("unexpected events: %+v", events)
	}
	for i, event := range events {
		if event.Sequence != int64(i+1) || event.RunID != run.ID() || event.SessionID != sessionID {
			t.Fatalf("invalid event envelope at %d: %+v", i, event)
		}
	}
	replayed, done, err := run.Wait(context.Background(), 0)
	if err != nil || !done || len(replayed) != len(events) {
		t.Fatalf("event replay failed: events=%d done=%t err=%v", len(replayed), done, err)
	}
}

func TestManagerAllowsOnlyOneActiveRunPerSession(t *testing.T) {
	manager, sessionID := runtimeTestManager(t, &runtimeProvider{block: true})
	run, err := manager.StartTurn(context.Background(), sessionID, "first", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartTurn(context.Background(), sessionID, "second", false); err == nil {
		t.Fatal("second active run was accepted")
	}
	if err := manager.Cancel(run.ID()); err != nil {
		t.Fatal(err)
	}
	events := waitForRun(t, run, nil)
	if events[len(events)-1].Type != EventRunCanceled {
		t.Fatalf("last event = %q, want canceled", events[len(events)-1].Type)
	}
}

func TestManagerRoutesApprovalDecisionBackToRun(t *testing.T) {
	manager, sessionID := runtimeTestManager(t, &runtimeProvider{toolFirst: true})
	run, err := manager.StartTurn(context.Background(), sessionID, "run a command", true)
	if err != nil {
		t.Fatal(err)
	}
	var approvalID string
	events := waitForRun(t, run, func(event Event) {
		if event.Type == EventApprovalRequested {
			approvalID = event.Approval.ID
			status, ok := manager.ActiveRun(sessionID)
			if !ok || status.ID != run.ID() || status.Done || len(status.PendingApprovals) != 1 || status.PendingApprovals[0].ID != approvalID {
				t.Errorf("unexpected active run status: ok=%t status=%+v", ok, status)
			}
			if err := manager.Approve(run.ID(), approvalID, agent.ApprovalDecision{Allow: true}); err != nil {
				t.Errorf("approve: %v", err)
			}
		}
	})
	if approvalID == "" {
		t.Fatal("approval event was not published")
	}
	seenResolved := false
	for _, event := range events {
		if event.Type == EventApprovalResolved && event.Decision != nil && event.Decision.Allow {
			seenResolved = true
		}
	}
	if !seenResolved || events[len(events)-1].Type != EventRunCompleted {
		t.Fatalf("approval lifecycle incomplete: %+v", events)
	}
	if _, ok := manager.ActiveRun(sessionID); ok {
		t.Fatal("completed run remained active")
	}
	status, ok := manager.RunStatus(run.ID())
	if !ok || !status.Done || status.CompletedAt == 0 || len(status.PendingApprovals) != 0 {
		t.Fatalf("unexpected completed run status: ok=%t status=%+v", ok, status)
	}
}

func TestManagerBoundsCompletedRuns(t *testing.T) {
	manager, sessionID := runtimeTestManagerWithOptions(t, &runtimeProvider{}, ManagerOptions{
		CompletedRunTTL:  time.Hour,
		MaxCompletedRuns: 1,
		MaxOpenSessions:  -1,
		MaxEventsPerRun:  32,
	})
	first, err := manager.StartTurn(context.Background(), sessionID, "first", false)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, nil)
	second, err := manager.StartTurn(context.Background(), sessionID, "second", false)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, second, nil)
	if _, ok := manager.Run(first.ID()); ok {
		t.Fatal("old completed run was retained past the configured limit")
	}
	if _, ok := manager.Run(second.ID()); !ok {
		t.Fatal("newest completed run was evicted")
	}
}

func TestManagerExpiresCompletedRunsByAge(t *testing.T) {
	start := time.Now()
	var nanos atomic.Int64
	nanos.Store(start.UnixNano())
	manager, sessionID := runtimeTestManagerWithOptions(t, &runtimeProvider{}, ManagerOptions{
		CompletedRunTTL:  time.Minute,
		MaxCompletedRuns: -1,
		MaxOpenSessions:  -1,
		MaxEventsPerRun:  32,
		Clock: func() time.Time {
			return time.Unix(0, nanos.Load())
		},
	})
	run, err := manager.StartTurn(context.Background(), sessionID, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, run, nil)
	nanos.Store(start.Add(2 * time.Minute).UnixNano())
	if _, ok := manager.Run(run.ID()); ok {
		t.Fatal("completed run was retained past its TTL")
	}
}

func TestManagerEvictsLeastRecentlyUsedInactiveSession(t *testing.T) {
	manager := NewManagerWithOptions(ManagerOptions{
		CompletedRunTTL:  time.Hour,
		MaxCompletedRuns: 8,
		MaxOpenSessions:  1,
		MaxEventsPerRun:  32,
	})
	firstID := addRuntimeService(t, manager, &runtimeProvider{})
	secondID := addRuntimeService(t, manager, &runtimeProvider{})
	if _, ok := manager.Get(firstID); ok {
		t.Fatal("least recently used inactive session was not evicted")
	}
	if _, ok := manager.Get(secondID); !ok {
		t.Fatal("newest session was evicted")
	}
}

func TestRunReportsExpiredEventHistory(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := newRun("session", cancel, 2, time.Now())
	run.publish(Event{Type: EventRunStarted})
	run.publish(Event{Type: EventAgent})
	run.publish(Event{Type: EventAgent})
	status := run.Status()
	if status.FirstSequence != 2 || status.LastSequence != 3 {
		t.Fatalf("unexpected retained window: %+v", status)
	}
	_, _, err := run.Wait(context.Background(), 0)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorEventHistoryExpired || !appErr.Retryable {
		t.Fatalf("unexpected expired-history error: %#v", err)
	}
	events, _, err := run.Wait(context.Background(), 1)
	if err != nil || len(events) != 2 || events[0].Sequence != 2 {
		t.Fatalf("retained event replay failed: events=%+v err=%v", events, err)
	}
}

func runtimeTestManager(t *testing.T, provider agent.Provider) (*Manager, string) {
	return runtimeTestManagerWithOptions(t, provider, ManagerOptions{})
}

func runtimeTestManagerWithOptions(t *testing.T, provider agent.Provider, options ManagerOptions) (*Manager, string) {
	t.Helper()
	manager := NewManagerWithOptions(options)
	sessionID := addRuntimeService(t, manager, provider)
	return manager, sessionID
}

func addRuntimeService(t *testing.T, manager *Manager, provider agent.Provider) string {
	t.Helper()
	cwd := t.TempDir()
	store, err := session.NewStore(filepath.Join(cwd, "session.jsonl"), cwd)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{
		Config:          config.Config{CWD: cwd, Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return provider },
		Store:           store,
		ModelRecorded:   true,
	})
	sessionID, err := manager.Add(service)
	if err != nil {
		t.Fatal(err)
	}
	return sessionID
}

func waitForRun(t *testing.T, run *Run, onEvent func(Event)) []Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var all []Event
	var after int64
	for {
		events, done, err := run.Wait(ctx, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			all = append(all, event)
			after = event.Sequence
			if onEvent != nil {
				onEvent(event)
			}
		}
		if done {
			return all
		}
	}
}
