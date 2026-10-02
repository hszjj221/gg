package cliapp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/workspace"
)

func TestWorkspaceMemoryStoreUsesOverlayForRegisteredCWD(t *testing.T) {
	dir := t.TempDir()
	// macOS: t.TempDir() can sit under a symlinked prefix (/var -> /private/var)
	// while the registry stores canonical roots. Resolve so the expectation
	// matches what the registry-backed lookup returns.
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	// Simulate startup auto-registration of the CWD.
	if _, _, err := workspace.EnsureDefaultWorkspace(home, dir); err != nil {
		t.Fatal(err)
	}
	global := memory.NewStore(filepath.Join(home, ".gg", "memory"))
	got := workspaceMemoryStore(config.Config{CWD: dir, HomeDir: home}, global)
	ov, ok := got.(*memory.Overlay)
	if !ok {
		t.Fatalf("registered CWD should get the workspace overlay, got %T", got)
	}
	if !ov.HasWorkspace() {
		t.Fatal("overlay should have a workspace layer")
	}
	if want := memory.OverlayDir(dir); ov.WorkspaceDir() != want {
		t.Fatalf("workspace dir = %q, want %q", ov.WorkspaceDir(), want)
	}
}

func TestWorkspaceMemoryStoreFallsBackToGlobal(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	// Corrupt the registry so Load fails: must fall back to global, never nil.
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "workspaces.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	global := memory.NewStore(filepath.Join(home, ".gg", "memory"))
	got := workspaceMemoryStore(config.Config{CWD: dir, HomeDir: home}, global)
	if got != memory.StoreAPI(global) {
		t.Fatalf("registry failure should fall back to the global store, got %T", got)
	}
}
