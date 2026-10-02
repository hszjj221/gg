package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalRootValid(t *testing.T) {
	dir := t.TempDir()
	got, err := canonicalRoot(dir)
	if err != nil {
		t.Fatalf("canonicalRoot(%q): %v", dir, err)
	}
	// TempDir may itself contain symlinks (e.g. /tmp on macOS); the canonical
	// form must at least be absolute.
	if !filepath.IsAbs(got) {
		t.Fatalf("canonicalRoot(%q) = %q, want absolute", dir, got)
	}
}

func TestCanonicalRootRejects(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"empty":    "",
		"relative": "some/relative/path",
		"missing":  filepath.Join(dir, "does-not-exist"),
		"file":     file,
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := canonicalRoot(root); err == nil {
				t.Fatalf("canonicalRoot(%q): want error, got nil", root)
			}
		})
	}
}

func TestCanonicalRootResolvesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test needs privileges on windows")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	got, err := canonicalRoot(link)
	if err != nil {
		t.Fatalf("canonicalRoot(%q): %v", link, err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonicalRoot(%q) = %q, want %q", link, got, want)
	}
}

func TestNewIDFormat(t *testing.T) {
	id := newID()
	if !strings.HasPrefix(id, "w_") || len(id) != 18 {
		t.Fatalf("newID() = %q, want w_ + 16 hex chars", id)
	}
	if id2 := newID(); id == id2 {
		t.Fatal("newID() returned the same ID twice")
	}
}

func TestRegistryPath(t *testing.T) {
	got := RegistryPath("/home/u")
	want := filepath.Join("/home/u", ".gg", "workspaces.json")
	if got != want {
		t.Fatalf("RegistryPath = %q, want %q", got, want)
	}
}
