package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/workspace"
)

func TestWorkspaceForkCreatesIndependentOpenSession(t *testing.T) {
	rt, root := runtimeForTest(t)
	source, err := rt.CreateSession("source")
	if err != nil {
		t.Fatal(err)
	}
	run, err := rt.StartTurn(context.Background(), source.SessionID, "question", false)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, run, nil)
	source, err = rt.Snapshot(source.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.TreeItems) < 2 {
		t.Fatalf("missing conversation tree: %+v", source.TreeItems)
	}
	update, err := rt.SessionAction(source.SessionID, SessionActionFork, source.TreeItems[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if update.SessionID == source.SessionID || update.Draft != "question" {
		t.Fatalf("fork did not create an independent session: %+v", update)
	}
	unchanged, err := rt.Snapshot(source.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.SessionID != source.SessionID || len(unchanged.Messages) != len(source.Messages) {
		t.Fatalf("fork mutated source session: before=%+v after=%+v", source, unchanged)
	}
	child, err := rt.Snapshot(update.SessionID)
	if err != nil || child.SessionID != update.SessionID {
		t.Fatalf("forked session was not registered: child=%+v err=%v", child, err)
	}
	data, err := json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), root) || strings.Contains(string(data), "sessionPath") {
		t.Fatalf("public snapshot leaked a filesystem path: %s", data)
	}
}

func TestWorkspaceListsAndRenamesSessions(t *testing.T) {
	rt, _ := runtimeForTest(t)
	snapshot, err := rt.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RenameSession(snapshot.SessionID, "renamed"); err != nil {
		t.Fatal(err)
	}
	items, err := rt.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != snapshot.SessionID || items[0].Name != "renamed" {
		t.Fatalf("unexpected session list: %+v", items)
	}
}

func TestWorkspaceNewSessionUsesJSONArrayCollections(t *testing.T) {
	rt, _ := runtimeForTest(t)
	snapshot, err := rt.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Messages == nil || snapshot.TreeItems == nil {
		t.Fatalf("new snapshot collections must be non-nil: %+v", snapshot)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"messages":[]`) || !strings.Contains(string(data), `"treeItems":[]`) {
		t.Fatalf("new snapshot must encode empty collections as arrays: %s", data)
	}
}

func TestWorkspaceInvalidatesSessionAfterExternalWriteConflict(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	repository := session.NewFileRepository(filepath.Join(root, "sessions"))
	registry := testRegistry(t)
	newRuntime := func() *Runtime {
		rt, err := NewRuntime(RuntimeOptions{
			Config:            config.Config{CWD: cwd, Selection: "test:model"},
			ProviderFactory:   func(config.Config) agent.Provider { return &runtimeProvider{} },
			WorkspaceRegistry: registry,
			NoSkills:          true,
			Repository:        repository,
		})
		if err != nil {
			t.Fatal(err)
		}
		return rt
	}
	first := newRuntime()
	created, err := first.CreateSession("initial")
	if err != nil {
		t.Fatal(err)
	}
	second := newRuntime()
	if _, err := second.OpenSession(created.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.RenameSession(created.SessionID, "external"); err != nil {
		t.Fatal(err)
	}
	_, err = second.RenameSession(created.SessionID, "stale")
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSessionConflict || !appErr.Retryable {
		t.Fatalf("unexpected conflict: %#v", err)
	}
	reloaded, err := second.Snapshot(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SessionName != "external" {
		t.Fatalf("stale service was not reopened: %+v", reloaded)
	}
}

func runtimeForTest(t *testing.T) (*Runtime, string) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	provider := &runtimeProvider{}
	rt, err := NewRuntime(RuntimeOptions{
		Config:            config.Config{CWD: cwd, Selection: "test:model"},
		ProviderFactory:   func(config.Config) agent.Provider { return provider },
		WorkspaceRegistry: testRegistry(t),
		NoSkills:          true,
		Repository:        session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rt, root
}

// testRegistry builds an empty workspace registry backed by a throwaway
// home directory. NewRuntime registers the configured CWD as the default
// workspace in memory; nothing is persisted.
func testRegistry(t *testing.T) *workspace.Registry {
	t.Helper()
	reg, err := workspace.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
