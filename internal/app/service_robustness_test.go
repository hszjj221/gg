package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

// blockingProvider signals when the first request arrives and then waits
// for release (or ctx cancellation) before answering.
type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
		return agent.AssistantMessage{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: "done"},
			StopReason: agent.StopReasonEndTurn,
		}, nil
	case <-ctx.Done():
		return agent.AssistantMessage{}, ctx.Err()
	}
}

// TestSnapshotDuringBlockedTurn guards the narrowed Service.mu: readers
// like Snapshot must not wait for an in-flight turn. The old code held the
// mutex across the whole Run, so a slow provider call blocked every
// concurrent reader for the duration of the turn.
func TestSnapshotDuringBlockedTurn(t *testing.T) {
	provider := &blockingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewService(Options{
		Config:          config.Config{CWD: t.TempDir(), Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return provider },
	})
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = service.Run(context.Background(), "hello", nil, nil)
	}()
	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider was not reached")
	}
	snapDone := make(chan Snapshot, 1)
	go func() { snapDone <- service.Snapshot() }()
	select {
	case <-snapDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Snapshot blocked on in-flight turn: Service.mu is held across provider calls")
	}
	close(provider.release)
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not finish after release")
	}
}

// failSummaryProvider fails compaction (summary) requests but answers
// normal requests, simulating a provider outage during auto-compact.
type failSummaryProvider struct{}

func (p *failSummaryProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	for _, m := range req.Messages {
		if m.Role == agent.RoleSystem && strings.Contains(m.Content, "compact conversation history") {
			return agent.AssistantMessage{}, errors.New("summary endpoint unavailable")
		}
	}
	return agent.AssistantMessage{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: "ok"},
		StopReason: agent.StopReasonEndTurn,
	}, nil
}

// TestAutoCompactionFailureFallsBackToTruncation: when summarization fails,
// auto-compact must degrade to hard truncation (explicit marker, prefix
// dropped) instead of failing the turn.
func TestAutoCompactionFailureFallsBackToTruncation(t *testing.T) {
	cfg := config.Config{CWD: t.TempDir(), Selection: "test:model"}
	// Budget fits the truncated tail (one ~500-token message plus the
	// marker) but not the full history, so auto-compact triggers.
	cfg.Context.MaxPromptTokens = 1000
	cfg.Context.AutoCompact = true
	cfg.Context.TailTurns = 1
	service := NewService(Options{Config: cfg})
	for i := 0; i < 6; i++ {
		if err := service.persistMessage(agent.Message{Role: agent.RoleUser, Content: strings.Repeat("q", 2000)}); err != nil {
			t.Fatal(err)
		}
		if err := service.persistMessage(agent.Message{Role: agent.RoleAssistant, Content: strings.Repeat("a", 2000)}); err != nil {
			t.Fatal(err)
		}
	}
	req, _, err := service.prepareRequest(context.Background(), &failSummaryProvider{}, nil, agent.Request{})
	if err != nil {
		t.Fatalf("prepareRequest with failed compaction should fall back to truncation, got: %v", err)
	}
	if got := service.summaryState().Text; got != truncationMarker {
		t.Fatalf("expected truncation marker summary, got %q", got)
	}
	// The rebuilt request must fit the budget: history starts after the
	// truncated prefix.
	if len(req.Messages) == 0 {
		t.Fatal("expected rebuilt request messages")
	}
	build := service.buildContext(nil, agent.Message{})
	if build.PromptTokens > cfg.Context.MaxPromptTokens {
		t.Fatalf("truncated context still over budget: %d > %d", build.PromptTokens, cfg.Context.MaxPromptTokens)
	}
}

// TestTruncateHistoryKeepsStoreMessages: the dropped prefix stays in the
// session store; only the in-memory window is truncated.
func TestTruncateHistoryKeepsStoreMessages(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(filepath.Join(cwd, "session.jsonl"), cwd)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{Config: config.Config{CWD: cwd, Selection: "test:model"}, Store: store})
	for i := 0; i < 4; i++ {
		if err := service.persistMessage(agent.Message{Role: agent.RoleUser, Content: "hello"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.truncateHistory(3); err != nil {
		t.Fatal(err)
	}
	if got := service.summaryState().ThroughMessageCount; got != 3 {
		t.Fatalf("ThroughMessageCount = %d, want 3", got)
	}
	// All four messages are still in the durable store.
	entries := store.TreeEntries()
	count := 0
	for _, e := range entries {
		if e.Message.Role == agent.RoleUser {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("store kept %d user messages, want 4", count)
	}
	// But the built context starts after the truncated prefix.
	build := service.buildContext(nil, agent.Message{})
	if build.KeptTurns != 1 {
		t.Fatalf("KeptTurns = %d, want 1", build.KeptTurns)
	}
}

// TestCheckoutRejectedDuringInFlightTurn: switching the conversation branch
// while a turn is in flight must be rejected. The runner keeps persisting
// into the store the turn started with, so a checkout underneath it would
// land the turn's messages on the wrong branch (P1).
func TestCheckoutRejectedDuringInFlightTurn(t *testing.T) {
	provider := &blockingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewService(Options{
		Config:          config.Config{CWD: t.TempDir(), Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return provider },
	})
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = service.Run(context.Background(), "hello", nil, nil)
	}()
	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider was not reached")
	}
	if _, err := service.Checkout("some-entry"); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("Checkout during run = %v, want in-progress error", err)
	}
	if _, err := service.HandleSessionAction(SessionActionTree, "some-entry"); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("HandleSessionAction during run = %v, want in-progress error", err)
	}
	close(provider.release)
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not finish after release")
	}
	// After the turn, checkout is attempted normally again (fails here only
	// because the entry does not exist — the in-progress guard is gone).
	if _, err := service.Checkout("some-entry"); err == nil || strings.Contains(err.Error(), "in progress") {
		t.Fatalf("Checkout after run = %v, want entry-not-found error", err)
	}
}

// cancelSummaryProvider cancels the turn's context on the first compaction
// request, then reports the cancellation.
type cancelSummaryProvider struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (p *cancelSummaryProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	for _, m := range req.Messages {
		if m.Role == agent.RoleSystem && strings.Contains(m.Content, "compact conversation history") {
			p.once.Do(func() { p.cancel() })
			<-ctx.Done()
			return agent.AssistantMessage{}, ctx.Err()
		}
	}
	return agent.AssistantMessage{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: "ok"},
		StopReason: agent.StopReasonEndTurn,
	}, nil
}

// TestAutoCompactionCanceledDoesNotTruncate: canceling a run while the
// summary request is in flight must propagate the cancellation, not persist
// the hard-truncation marker. Truncating would make later turns skip the
// whole prefix even though nothing was summarized (P2).
func TestAutoCompactionCanceledDoesNotTruncate(t *testing.T) {
	cfg := config.Config{CWD: t.TempDir(), Selection: "test:model"}
	cfg.Context.MaxPromptTokens = 1000
	cfg.Context.AutoCompact = true
	cfg.Context.TailTurns = 1
	service := NewService(Options{Config: cfg})
	for i := 0; i < 6; i++ {
		if err := service.persistMessage(agent.Message{Role: agent.RoleUser, Content: strings.Repeat("q", 2000)}); err != nil {
			t.Fatal(err)
		}
		if err := service.persistMessage(agent.Message{Role: agent.RoleAssistant, Content: strings.Repeat("a", 2000)}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err := service.prepareRequest(ctx, &cancelSummaryProvider{cancel: cancel}, nil, agent.Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("prepareRequest with canceled compaction = %v, want context.Canceled", err)
	}
	if got := service.summaryState().Text; got == truncationMarker {
		t.Fatal("canceled compaction must not persist the truncation marker")
	}
	if got := service.summaryState().ThroughMessageCount; got != 0 {
		t.Fatalf("ThroughMessageCount = %d, want 0 (nothing was summarized)", got)
	}
}
