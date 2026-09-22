package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

func TestWorkspaceForkCreatesIndependentOpenSession(t *testing.T) {
	workspace, root := workspaceForTest(t)
	source, err := workspace.CreateSession("source")
	if err != nil {
		t.Fatal(err)
	}
	run, err := workspace.StartTurn(context.Background(), source.SessionID, "question", false)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, run, nil)
	source, err = workspace.Snapshot(source.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.TreeItems) < 2 {
		t.Fatalf("missing conversation tree: %+v", source.TreeItems)
	}
	update, err := workspace.SessionAction(source.SessionID, SessionActionFork, source.TreeItems[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if update.SessionID == source.SessionID || update.Draft != "question" {
		t.Fatalf("fork did not create an independent session: %+v", update)
	}
	unchanged, err := workspace.Snapshot(source.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.SessionID != source.SessionID || len(unchanged.Messages) != len(source.Messages) {
		t.Fatalf("fork mutated source session: before=%+v after=%+v", source, unchanged)
	}
	child, err := workspace.Snapshot(update.SessionID)
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
	workspace, _ := workspaceForTest(t)
	snapshot, err := workspace.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RenameSession(snapshot.SessionID, "renamed"); err != nil {
		t.Fatal(err)
	}
	items, err := workspace.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != snapshot.SessionID || items[0].Name != "renamed" {
		t.Fatalf("unexpected session list: %+v", items)
	}
}

func TestWorkspaceNewSessionUsesJSONArrayCollections(t *testing.T) {
	workspace, _ := workspaceForTest(t)
	snapshot, err := workspace.CreateSession("")
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
	repository := session.NewFileRepository(filepath.Join(root, "sessions"))
	newWorkspace := func() *Workspace {
		workspace, err := NewWorkspace(WorkspaceOptions{
			Config:          config.Config{CWD: cwd, Selection: "test:model"},
			ProviderFactory: func(config.Config) agent.Provider { return &runtimeProvider{} },
			Repository:      repository,
		})
		if err != nil {
			t.Fatal(err)
		}
		return workspace
	}
	first := newWorkspace()
	created, err := first.CreateSession("initial")
	if err != nil {
		t.Fatal(err)
	}
	second := newWorkspace()
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

func workspaceForTest(t *testing.T) (*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	provider := &runtimeProvider{}
	workspace, err := NewWorkspace(WorkspaceOptions{
		Config:          config.Config{CWD: cwd, Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return provider },
		Repository:      session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, root
}
