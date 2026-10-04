package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
	"github.com/hszjj221/gg/internal/session"
)

func TestSessionFactoryModelSelectionPolicy(t *testing.T) {
	dir := t.TempDir()
	cfg, err := (config.Config{CWD: dir, HomeDir: dir, Providers: map[string]config.ProviderConfig{"test": {Models: []string{"default", "saved", "override"}}}}).WithSelection("test:default")
	if err != nil {
		t.Fatal(err)
	}
	loaded := session.Loaded{LastModel: &session.ModelEntry{Selection: "test:saved"}, Messages: []agent.Message{{Role: agent.RoleUser, Content: "history"}}}
	for _, tc := range []struct {
		name, override, want string
		recorded             bool
	}{
		{"restore", "", "test:saved", true},
		{"explicit override", "test:override", "test:override", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewSessionService(Options{Config: cfg, ToolProviders: []ToolProvider{}}, nil, loaded, tc.override)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()
			if got := svc.Snapshot(); got.ModelName != tc.want || len(got.Messages) != 1 || svc.modelRecorded != tc.recorded {
				t.Fatalf("restored state = %+v, recorded=%v", got, svc.modelRecorded)
			}
		})
	}
	loaded.LastModel.Selection = "missing:model"
	if _, err := NewSessionService(Options{Config: cfg}, nil, loaded, ""); err == nil {
		t.Fatal("unknown persisted provider must fail at session initialization")
	}
}

func TestSessionFactoryRejectsInvalidSummaryPosition(t *testing.T) {
	loaded := session.Loaded{LastSummary: &session.SummaryEntry{Summary: "summary", ThroughMessageCount: 2}, Messages: []agent.Message{{Role: agent.RoleUser, Content: "one message"}}}
	if _, err := NewSessionService(Options{}, nil, loaded, ""); err == nil {
		t.Fatal("invalid summary was silently accepted")
	}
}

func TestClonePreservesDisabledToolRegistry(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(Options{Config: config.Config{CWD: dir, Selection: "test:model"}, Store: store, ToolProviders: []ToolProvider{}})
	defer svc.Close()
	child, _, err := svc.Clone()
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if got := child.buildTools(context.Background(), nil); len(got) != 0 {
		t.Fatalf("clone re-enabled %d built-in tools", len(got))
	}
}

func TestServiceSerializesDirectTurns(t *testing.T) {
	provider := &blockingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(Options{Config: config.Config{CWD: t.TempDir(), Selection: "test:model"}, ToolProviders: []ToolProvider{}, ProviderFactory: func(config.Config) agent.Provider { return provider }})
	defer svc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := svc.Run(ctx, "first", nil, nil); done <- err }()
	select {
	case <-provider.entered:
	case <-ctx.Done():
		t.Fatal("first turn did not enter provider")
	}
	_, err := svc.Run(ctx, "second", nil, nil)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorRunConflict {
		t.Fatalf("concurrent direct turn = %v", err)
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(svc.Snapshot().Messages) != 2 {
		t.Fatal("rejected turn changed conversation history")
	}
}

func TestRunAwaitSurvivesExpiredTerminalEvent(t *testing.T) {
	run := newRun("session", func() {}, 16, time.Now())
	run.replay.maxBytes = 2048
	run.complete(Result{Content: "success", ModelName: "test:model", TreeItems: []TreeItem{{Text: strings.Repeat("x", 4096)}}}, nil, time.Now())
	if _, _, err := run.Wait(context.Background(), 0); err == nil {
		t.Fatal("oversized terminal event must still expire transport replay")
	}
	result, err := run.Await(context.Background())
	if err != nil || result.Content != "success" || result.ModelName != "test:model" {
		t.Fatalf("execution outcome lost: %+v, %v", result, err)
	}
	if result.TreeItems != nil {
		t.Fatal("execution outcome should not retain the session tree")
	}
}

func TestRunAwaitPreservesFailureCause(t *testing.T) {
	run := newRun("session", func() {}, 1, time.Now())
	run.replay.maxBytes = 1
	run.complete(Result{Content: "partial"}, session.ErrConflict, time.Now())
	result, err := run.Await(context.Background())
	if result.Content != "partial" || !errors.Is(err, session.ErrConflict) {
		t.Fatalf("partial result or cause lost: %+v, %v", result, err)
	}
}

func TestRunAwaitCancellationDoesNotCancelExecution(t *testing.T) {
	run := newRun("session", func() { t.Error("waiting canceled the run") }, 16, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run.Await(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait cancellation = %v", err)
	}
	resultCh := make(chan Result, 1)
	go func() {
		result, _ := run.Await(context.Background())
		resultCh <- result
	}()
	run.complete(Result{Content: "done"}, nil, time.Now())
	select {
	case result := <-resultCh:
		if result.Content != "done" {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not wake waiter")
	}
}

func TestModelWriteConflictPreservesMemorySelection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	cfg, err := (config.Config{CWD: dir, HomeDir: dir, Providers: map[string]config.ProviderConfig{"test": {Models: []string{"old", "new"}}}}).WithSelection("test:old")
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendModel("test", "old"); err != nil {
		t.Fatal(err)
	}
	svc := NewService(Options{Config: cfg, Store: store, ModelRecorded: true})
	t.Cleanup(func() { _ = svc.Close() })
	other, err := session.NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AppendName("external writer"); err != nil {
		t.Fatal(err)
	}
	_, handled, err := svc.handleModelCommand("/model test:new")
	if !handled || !errors.Is(err, session.ErrConflict) {
		t.Fatalf("expected write conflict: handled=%v, err=%v", handled, err)
	}
	if got := svc.Snapshot().ModelName; got != "test:old" {
		t.Fatalf("failed switch changed memory to %q", got)
	}
	loaded, err := session.Load(path)
	if err != nil || loaded.LastModel.Selection != "test:old" {
		t.Fatalf("durable selection = %+v, %v", loaded.LastModel, err)
	}
}

type changingPublishSource struct{ *artifact.Store }

func (s changingPublishSource) Publish(id string, version int) (string, int, error) {
	if _, err := s.Store.AddVersion(id, "new draft"); err != nil {
		return "", 0, err
	}
	return s.Store.Publish(id, version)
}

type failingPublishCollection struct {
	*library.Store
	addErr, removeErr error
}

func (c failingPublishCollection) AddBytes(name string, data []byte, source string) (*library.Entry, error) {
	if c.addErr != nil {
		return nil, c.addErr
	}
	return c.Store.AddBytes(name, data, source)
}

func (c failingPublishCollection) Remove(id string) error {
	if c.removeErr != nil {
		return c.removeErr
	}
	return c.Store.Remove(id)
}

func TestPublishCompensatesConcurrentVersionChange(t *testing.T) {
	rt, store := testArtifactWorkspace(t)
	a, err := store.Create("Report", artifact.TypeMarkdown, "selected draft")
	if err != nil {
		t.Fatal(err)
	}
	_, err = PublishArtifact(changingPublishSource{store}, rt.libraryStore, a.ID)
	if !errors.Is(err, artifact.ErrVersionChanged) {
		t.Fatalf("publish conflict = %v", err)
	}
	entries, err := rt.libraryStore.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphan copy remains: %+v, %v", entries, err)
	}
	a, err = store.Get(a.ID)
	if err != nil || a.PublishedVersion != 0 || a.Version != 2 {
		t.Fatalf("artifact = %+v, %v", a, err)
	}
}

func TestPublishLibraryFailureDoesNotMarkArtifact(t *testing.T) {
	rt, store := testArtifactWorkspace(t)
	a, err := store.Create("Report", artifact.TypeMarkdown, "draft")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("library unavailable")
	_, err = PublishArtifact(store, failingPublishCollection{Store: rt.libraryStore, addErr: cause}, a.ID)
	if !errors.Is(err, cause) {
		t.Fatalf("publish error = %v", err)
	}
	a, err = store.Get(a.ID)
	if err != nil || a.PublishedVersion != 0 {
		t.Fatalf("artifact marked despite failed copy: %+v, %v", a, err)
	}
}

func TestPublishReportsFailedCompensation(t *testing.T) {
	rt, store := testArtifactWorkspace(t)
	a, err := store.Create("Report", artifact.TypeMarkdown, "draft")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("cannot remove library copy")
	_, err = PublishArtifact(changingPublishSource{store}, failingPublishCollection{Store: rt.libraryStore, removeErr: cause}, a.ID)
	if !errors.Is(err, artifact.ErrVersionChanged) || !errors.Is(err, cause) {
		t.Fatalf("publish must report conflict and failed compensation: %v", err)
	}
}
