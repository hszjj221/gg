package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlinked MEMORY.md in the workspace layer must not be followed:
// reads fail closed instead of pulling external content into the prompt.
func TestWorkspaceStoreRejectsSymlinkedMemoryFile(t *testing.T) {
	root := t.TempDir()
	wsDir := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secret, []byte("top-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(wsDir, "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	global := NewStore(filepath.Join(root, "g"))
	o := NewOverlay(wsDir, global)
	if _, err := o.LoadCurated(1000); err == nil {
		t.Fatal("LoadCurated must reject a symlinked workspace MEMORY.md")
	}
	// Appends must not corrupt the symlink target either.
	if err := o.AppendCurated("hello"); err == nil {
		t.Fatal("AppendCurated must reject a symlinked workspace MEMORY.md")
	}
	if data, _ := os.ReadFile(secret); string(data) != "top-secret" {
		t.Fatalf("symlink target was modified: %q", data)
	}
	// Search must skip the symlinked file.
	if _, err := o.SearchLayered("top-secret", "all"); err != nil {
		t.Fatalf("SearchLayered must not fail on symlinked files: %v", err)
	}
}

// A symlinked workspace layer directory is rejected outright.
func TestWorkspaceStoreRejectsSymlinkedLayerDir(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	global := NewStore(filepath.Join(root, "g"))
	o := NewOverlay(link, global)
	if _, err := o.LoadCurated(1000); err == nil {
		t.Fatal("LoadCurated must reject a symlinked workspace layer dir")
	}
	if _, err := o.SearchLayered("x", "all"); err == nil {
		t.Fatal("SearchLayered must reject a symlinked workspace layer dir")
	}
}

// The global store keeps its historical behavior: symlinks are followed.
// (Users intentionally symlink ~/.gg/memory elsewhere.)
func TestGlobalStoreStillFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	gDir := filepath.Join(root, "g")
	if err := os.MkdirAll(gDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.md")
	if err := os.WriteFile(target, []byte("global content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(gDir, "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	s := NewStore(gDir)
	snap, err := s.LoadCurated(1000)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Content != "global content" {
		t.Fatalf("global store should follow symlinks, got %q", snap.Content)
	}
}
