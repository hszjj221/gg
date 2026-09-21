package app

import (
	"context"
	"path/filepath"
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
}

func runtimeTestManager(t *testing.T, provider agent.Provider) (*Manager, string) {
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
	manager := NewManager()
	sessionID, err := manager.Add(service)
	if err != nil {
		t.Fatal(err)
	}
	return manager, sessionID
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
