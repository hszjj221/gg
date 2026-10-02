package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentDirIsPurePathComputation(t *testing.T) {
	root := t.TempDir()
	got := AgentDir(root)
	want := filepath.Join(root, ".gg", "agent")
	if got != want {
		t.Fatalf("AgentDir(%q) = %q, want %q", root, got, want)
	}
	// Pure computation: nothing is created on disk.
	if _, err := os.Stat(filepath.Join(root, ".gg")); !os.IsNotExist(err) {
		t.Fatalf("AgentDir must not touch the filesystem")
	}
}

func TestEnsureAgentDirCreatesLazilyWith0700(t *testing.T) {
	root := t.TempDir()
	dir, err := EnsureAgentDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if dir != AgentDir(root) {
		t.Fatalf("EnsureAgentDir returned %q, want %q", dir, AgentDir(root))
	}
	checkPerm := func(path string) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", path)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("%q has permissions %04o, want 0700", path, perm)
		}
	}
	checkPerm(dir)
	checkPerm(filepath.Join(root, ".gg"))

	// Second call is a no-op, not an error.
	if _, err := EnsureAgentDir(root); err != nil {
		t.Fatalf("second EnsureAgentDir failed: %v", err)
	}
}

func TestInAgentDirBoundaries(t *testing.T) {
	root := t.TempDir()
	agentDir := AgentDir(root)
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"agent dir itself", agentDir, true},
		{"file inside", filepath.Join(agentDir, "draft.txt"), true},
		{"nested inside", filepath.Join(agentDir, "dl", "a", "b.bin"), true},
		{"parent .gg dir", filepath.Join(root, ".gg"), false},
		// Boundary: a sibling directory whose name starts with "agent"
		// must not count as the agent area.
		{"agent-evil sibling", filepath.Join(root, ".gg", "agent-evil", "x.txt"), false},
		{"agent2 sibling", filepath.Join(root, ".gg", "agent2"), false},
		{"file at workspace root", filepath.Join(root, "README.md"), false},
		{"outside root", filepath.Join(t.TempDir(), "x.txt"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InAgentDir(root, tc.path); got != tc.want {
				t.Fatalf("InAgentDir(%q, %q) = %v, want %v", root, tc.path, got, tc.want)
			}
		})
	}
}

func TestInAgentDirCleansDotDot(t *testing.T) {
	root := t.TempDir()
	// A path that lexically escapes but resolves back inside still counts;
	// InAgentDir is a lexical check on the cleaned path, matching the
	// resolver's own view of the target.
	inside := filepath.Join(AgentDir(root), "sub", "..", "draft.txt")
	if !InAgentDir(root, inside) {
		t.Fatalf("InAgentDir(%q, %q) = false, want true", root, inside)
	}
	escape := filepath.Join(AgentDir(root), "..", "other.txt")
	if InAgentDir(root, escape) {
		t.Fatalf("InAgentDir(%q, %q) = true, want false", root, escape)
	}
}

func TestEnsureRealAgentDirRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "src")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant .gg/agent as a symlink into the workspace.
	ggDir := filepath.Join(root, ".gg")
	if err := os.MkdirAll(ggDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(ggDir, "agent")); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureRealAgentDir(root); err == nil {
		t.Fatal("EnsureRealAgentDir must refuse a symlinked agent area")
	}
	if InRealAgentDir(root, filepath.Join(root, ".gg", "agent", "main.go")) {
		t.Fatal("InRealAgentDir must not grant the exemption for a symlinked agent area")
	}
}

func TestInRealAgentDirResolvesTarget(t *testing.T) {
	root := t.TempDir()
	// macOS: t.TempDir() can sit under a symlinked prefix (/var -> /private/var)
	// while EnsureRealAgentDir returns the resolved path. Resolve the root so
	// both sides of the containment check use the same spelling.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	realDir, err := EnsureRealAgentDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// A real file inside the agent area qualifies.
	p := filepath.Join(realDir, "draft.md")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !InRealAgentDir(root, p) {
		t.Fatalf("InRealAgentDir(%q) = false, want true", p)
	}
	// A symlinked subdirectory escaping the agent area does not.
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(realDir, "link")); err != nil {
		t.Fatal(err)
	}
	if InRealAgentDir(root, filepath.Join(realDir, "link", "main.go")) {
		t.Fatal("InRealAgentDir must not grant the exemption through a symlinked subdirectory")
	}
	// Outside the agent area never qualifies.
	if InRealAgentDir(root, filepath.Join(root, "src", "main.go")) {
		t.Fatal("InRealAgentDir must be false outside the agent area")
	}
}
