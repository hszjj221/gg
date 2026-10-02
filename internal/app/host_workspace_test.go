package app

import (
	"context"
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

// twoWorkspaceRuntime builds a runtime with two registered workspaces:
// "ws-a" (the default, matching cfg.CWD) and "ws-b".
func twoWorkspaceRuntime(t *testing.T) (*Runtime, workspace.Workspace, workspace.Workspace, *session.FileRepository) {
	t.Helper()
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	for _, dir := range []string{dirA, dirB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := workspace.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wsA, err := reg.Add("ws-a", dirA)
	if err != nil {
		t.Fatal(err)
	}
	wsB, err := reg.Add("ws-b", dirB)
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewFileRepository(filepath.Join(root, "sessions"))
	rt, err := NewRuntime(RuntimeOptions{
		Config:            config.Config{CWD: dirA, Selection: "test:model"},
		ProviderFactory:   func(config.Config) agent.Provider { return &runtimeProvider{} },
		WorkspaceRegistry: reg,
		NoSkills:          true,
		Repository:        repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rt, wsA, wsB, repo
}

func appCode(t *testing.T, err error) ErrorCode {
	t.Helper()
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T: %v", err, err)
	}
	return appErr.Code
}

func TestRuntimeWorkspaceIsolation(t *testing.T) {
	rt, wsA, wsB, repo := twoWorkspaceRuntime(t)
	if got := rt.DefaultWorkspace(); got.ID != wsA.ID {
		t.Fatalf("default workspace = %q, want %q", got.ID, wsA.ID)
	}
	snapA, err := rt.CreateSessionInWorkspace("a-one", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	snapB, err := rt.CreateSessionInWorkspace("b-one", wsB.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Sessions are invisible outside their own workspace root.
	infosA, err := repo.List(wsA.Root)
	if err != nil {
		t.Fatal(err)
	}
	infosB, err := repo.List(wsB.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(infosA) != 1 || infosA[0].ID != snapA.SessionID {
		t.Fatalf("ws-a sessions = %+v, want only %s", infosA, snapA.SessionID)
	}
	if len(infosB) != 1 || infosB[0].ID != snapB.SessionID {
		t.Fatalf("ws-b sessions = %+v, want only %s", infosB, snapB.SessionID)
	}
	// Each service is bound to its workspace's state and root.
	stA, svcA, err := rt.service(snapA.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stA.ws.ID != wsA.ID || svcA.cfg.CWD != wsA.Root {
		t.Fatalf("ws-a service bound to workspace %q root %q", stA.ws.ID, svcA.cfg.CWD)
	}
	stB, svcB, err := rt.service(snapB.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stB.ws.ID != wsB.ID || svcB.cfg.CWD != wsB.Root {
		t.Fatalf("ws-b service bound to workspace %q root %q", stB.ws.ID, svcB.cfg.CWD)
	}
	// CreateSession without a ref lands in the default workspace.
	snapDefault, err := rt.CreateSession("plain")
	if err != nil {
		t.Fatal(err)
	}
	infosA, err = repo.List(wsA.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(infosA) != 2 {
		t.Fatalf("default session did not land in ws-a: %+v", infosA)
	}
	st, _, err := rt.service(snapDefault.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if st.ws.ID != wsA.ID {
		t.Fatalf("default session bound to workspace %q, want %q", st.ws.ID, wsA.ID)
	}
}

func TestRuntimeOpenSessionAcrossWorkspaces(t *testing.T) {
	rt, wsA, wsB, repo := twoWorkspaceRuntime(t)
	snapA, err := rt.CreateSessionInWorkspace("a-one", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	// A fresh runtime (default workspace ws-b) still opens the ws-a
	// session: lookup searches every registered workspace in order.
	rt2, err := NewRuntime(RuntimeOptions{
		Config:            config.Config{CWD: wsB.Root, Selection: "test:model"},
		ProviderFactory:   func(config.Config) agent.Provider { return &runtimeProvider{} },
		WorkspaceRegistry: rt.WorkspaceRegistry(),
		NoSkills:          true,
		Repository:        repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := rt2.OpenSession(snapA.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.SessionID != snapA.SessionID {
		t.Fatalf("opened wrong session: %+v", opened)
	}
	st, _, err := rt2.service(snapA.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if st.ws.ID != wsA.ID {
		t.Fatalf("cross-workspace open bound to %q, want %q", st.ws.ID, wsA.ID)
	}
}

func TestRuntimeBackfillsLegacySession(t *testing.T) {
	rt, wsA, _, repo := twoWorkspaceRuntime(t)
	// Simulate a pre-workspace session: created directly through the
	// repository, so its header carries no workspace ID.
	_, loaded, err := repo.Create(wsA.Root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.WorkspaceID != "" {
		t.Fatalf("fresh session unexpectedly bound: %q", loaded.Header.WorkspaceID)
	}
	if _, err := rt.OpenSession(loaded.Header.ID); err != nil {
		t.Fatal(err)
	}
	// The workspace ID must be backfilled into the file header itself.
	reopened, _, err := repo.OpenForCWD(wsA.Root, loaded.Header.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(reopened.Path())
	if err != nil {
		t.Fatal(err)
	}
	header := strings.SplitN(string(data), "\n", 2)[0]
	if !strings.Contains(header, `"workspaceId":"`+wsA.ID+`"`) {
		t.Fatalf("header was not backfilled: %s", header)
	}
	// Reopening is stable: the recorded ID resolves without backfill.
	st, _, err := rt.service(loaded.Header.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.ws.ID != wsA.ID {
		t.Fatalf("backfilled session bound to %q, want %q", st.ws.ID, wsA.ID)
	}
}

func TestRuntimeStartTurnInWorkspace(t *testing.T) {
	rt, wsA, _, _ := twoWorkspaceRuntime(t)
	snap, err := rt.CreateSessionInWorkspace("happy", "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	run, err := rt.StartTurnInWorkspace(context.Background(), snap.SessionID, "question", false, wsA.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, run, nil)
	if _, err := rt.RunStatus(run.ID()); err != nil {
		t.Fatalf("run not found across workspace states: %v", err)
	}
}

func TestRuntimeStartTurnInWorkspaceErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("unknown workspace", func(t *testing.T) {
		rt, _, _, _ := twoWorkspaceRuntime(t)
		snap, err := rt.CreateSession("s")
		if err != nil {
			t.Fatal(err)
		}
		_, err = rt.StartTurnInWorkspace(ctx, snap.SessionID, "hi", false, "nope")
		if code := appCode(t, err); code != ErrorWorkspaceMismatch {
			t.Fatalf("code = %q, want %q", code, ErrorWorkspaceMismatch)
		}
	})
	t.Run("session open in another workspace", func(t *testing.T) {
		rt, wsA, wsB, _ := twoWorkspaceRuntime(t)
		snap, err := rt.CreateSessionInWorkspace("s", "ws-a")
		if err != nil {
			t.Fatal(err)
		}
		// Open the session so it is bound to ws-a's state.
		if _, err := rt.Snapshot(snap.SessionID); err != nil {
			t.Fatal(err)
		}
		_, err = rt.StartTurnInWorkspace(ctx, snap.SessionID, "hi", false, "ws-b")
		if code := appCode(t, err); code != ErrorWorkspaceMismatch {
			t.Fatalf("code = %q, want %q", code, ErrorWorkspaceMismatch)
		}
		// The error names the owning workspace but never its root path.
		if !strings.Contains(err.Error(), wsA.Name) {
			t.Fatalf("error does not name the owning workspace: %v", err)
		}
		if strings.Contains(err.Error(), wsA.Root) || strings.Contains(err.Error(), wsB.Root) {
			t.Fatalf("error leaked a workspace root path: %v", err)
		}
	})
	t.Run("session file bound to another workspace", func(t *testing.T) {
		rt, wsA, wsB, repo := twoWorkspaceRuntime(t)
		// A session file under ws-b's root whose header claims ws-a.
		store, loaded, err := repo.Create(wsB.Root)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetWorkspaceID(wsA.ID); err != nil {
			t.Fatal(err)
		}
		_, err = rt.StartTurnInWorkspace(ctx, loaded.Header.ID, "hi", false, "ws-b")
		if code := appCode(t, err); code != ErrorWorkspaceMismatch {
			t.Fatalf("code = %q, want %q", code, ErrorWorkspaceMismatch)
		}
		if !strings.Contains(err.Error(), wsA.Name) {
			t.Fatalf("error does not name the owning workspace: %v", err)
		}
		if strings.Contains(err.Error(), wsA.Root) || strings.Contains(err.Error(), wsB.Root) {
			t.Fatalf("error leaked a workspace root path: %v", err)
		}
	})
	t.Run("unknown session in known workspace", func(t *testing.T) {
		rt, _, _, _ := twoWorkspaceRuntime(t)
		_, err := rt.StartTurnInWorkspace(ctx, "does-not-exist", "hi", false, "ws-b")
		if code := appCode(t, err); code != ErrorSessionNotFound {
			t.Fatalf("code = %q, want %q", code, ErrorSessionNotFound)
		}
	})
}

func TestRuntimeResolveWorkspace(t *testing.T) {
	rt, wsA, wsB, _ := twoWorkspaceRuntime(t)
	if ws, err := rt.ResolveWorkspace(""); err != nil || ws.ID != wsA.ID {
		t.Fatalf("empty ref = %+v, %v; want default %q", ws, err, wsA.ID)
	}
	if ws, err := rt.ResolveWorkspace(wsB.ID); err != nil || ws.ID != wsB.ID {
		t.Fatalf("id ref = %+v, %v; want %q", ws, err, wsB.ID)
	}
	if ws, err := rt.ResolveWorkspace("ws-b"); err != nil || ws.ID != wsB.ID {
		t.Fatalf("name ref = %+v, %v; want %q", ws, err, wsB.ID)
	}
	if _, err := rt.ResolveWorkspace("nope"); appCode(t, err) != ErrorWorkspaceMismatch {
		t.Fatalf("unknown ref err = %v", err)
	}
	if _, err := rt.CreateSessionInWorkspace("x", "nope"); appCode(t, err) != ErrorWorkspaceMismatch {
		t.Fatalf("create in unknown workspace err = %v", err)
	}
}

func TestRuntimeDegradedProvidersUnion(t *testing.T) {
	rt, wsA, wsB, _ := twoWorkspaceRuntime(t)
	stA, err := rt.getState(wsA.ID)
	if err != nil {
		t.Fatal(err)
	}
	stB, err := rt.getState(wsB.ID)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	stA.degraded.Report("beta", "unhealthy", boom)
	stB.degraded.Report("alpha", "unhealthy", boom)
	stB.degraded.Report("beta", "other reason", boom)
	got := rt.DegradedProviders()
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("union = %+v, want sorted [alpha beta]", got)
	}
	// A recovery in any state clears the provider everywhere in the union.
	stA.degraded.Report("alpha", "", nil)
	stB.degraded.Report("alpha", "", nil)
	got = rt.DegradedProviders()
	if len(got) != 1 || got[0].Name != "beta" {
		t.Fatalf("union after recovery = %+v, want [beta]", got)
	}
}

func TestOpenSessionAnywhereSkipsCorruptWorkspace(t *testing.T) {
	root := t.TempDir()
	dirA := filepath.Join(root, "a")
	dirB := filepath.Join(root, "b")
	for _, dir := range []string{dirA, dirB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sessionRoot := filepath.Join(root, "sessions")
	reg, err := workspace.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wsA, err := reg.Add("ws-a", dirA)
	if err != nil {
		t.Fatal(err)
	}
	wsB, err := reg.Add("ws-b", dirB)
	if err != nil {
		t.Fatal(err)
	}
	repo := session.NewFileRepository(sessionRoot)
	newRuntime := func() *Runtime {
		t.Helper()
		rt, err := NewRuntime(RuntimeOptions{
			Config:            config.Config{CWD: dirA, Selection: "test:model"},
			ProviderFactory:   func(config.Config) agent.Provider { return &runtimeProvider{} },
			WorkspaceRegistry: reg,
			NoSkills:          true,
			Repository:        repo,
		})
		if err != nil {
			t.Fatal(err)
		}
		return rt
	}
	rt1 := newRuntime()
	snapB, err := rt1.CreateSessionInWorkspace("b-one", wsB.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Poison ws-a's session directory: ListForCWD loads every .jsonl, so
	// one corrupt file used to abort the whole multi-workspace probe.
	dirAKey := session.CWDDir(sessionRoot, wsA.Root)
	if err := os.MkdirAll(dirAKey, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirAKey, "corrupt.jsonl"), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fresh runtime (nothing cached in memory) must still reach the
	// session in ws-b.
	rt2 := newRuntime()
	st, svc, err := rt2.openSessionAnywhere(snapB.SessionID)
	if err != nil {
		t.Fatalf("openSessionAnywhere with corrupt ws-a: %v", err)
	}
	if st.ws.ID != wsB.ID || svc.cfg.CWD != wsB.Root {
		t.Fatalf("resolved to workspace %q root %q, want ws-b", st.ws.ID, svc.cfg.CWD)
	}
}
