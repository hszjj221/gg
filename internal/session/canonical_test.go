package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCWDDirCanonicalizesSpellings(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if a, b := CWDDir(root, target), CWDDir(root, link); a != b {
		t.Fatalf("symlinked spellings map to different dirs: %q vs %q", a, b)
	}
	// "." must resolve against the process working directory instead of
	// keying the session root itself.
	if got := CWDDir(root, "."); !filepath.IsAbs(got) || got == root {
		t.Fatalf("CWDDir(%q, %q) = %q, want an absolute subdirectory", root, ".", got)
	}
}

func TestFindSessionAnywhere(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	repo := NewFileRepository(sessions)

	// Legacy layout 1: keyed by "." — files directly under the session root.
	_, topLoaded, err := repo.OpenPath(filepath.Join(sessions, "1700000000000000000.jsonl"), ".")
	if err != nil {
		t.Fatal(err)
	}
	// Legacy layout 2: keyed by a raw (non-canonical) subdir spelling.
	legacyDir := filepath.Join(sessions, "raw-spelling")
	_, subLoaded, err := repo.OpenPath(filepath.Join(legacyDir, "1700000000000000001.jsonl"), "/raw/spelling")
	if err != nil {
		t.Fatal(err)
	}
	// A corrupt file must not break the scan.
	if err := os.WriteFile(filepath.Join(sessions, "corrupt.jsonl"), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		id      string
		wantDir string
	}{
		{topLoaded.Header.ID, sessions},
		{subLoaded.Header.ID, legacyDir},
	} {
		got, err := FindSessionAnywhere(sessions, tc.id)
		if err != nil {
			t.Fatalf("FindSessionAnywhere(%q): %v", tc.id, err)
		}
		if filepath.Dir(got) != tc.wantDir {
			t.Fatalf("FindSessionAnywhere(%q) = %q, want dir %q", tc.id, got, tc.wantDir)
		}
	}
	if _, err := FindSessionAnywhere(sessions, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindSessionAnywhere(miss) = %v, want ErrNotFound", err)
	}
	if _, err := FindSessionAnywhere(sessions, "  "); err == nil {
		t.Fatal("FindSessionAnywhere(empty) = nil, want error")
	}
}

func TestOpenForCWDFallsBackToAnywhere(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	repo := NewFileRepository(sessions)
	// Written the legacy way: raw "." key, file directly under the root.
	_, legacy, err := repo.OpenPath(filepath.Join(sessions, "legacy.jsonl"), ".")
	if err != nil {
		t.Fatal(err)
	}
	// A lookup by the canonical spelling misses the canonical directory
	// but must still find the session through the fallback.
	opened, loaded, err := repo.OpenForCWD(t.TempDir(), legacy.Header.ID, false)
	if err != nil {
		t.Fatalf("OpenForCWD fallback: %v", err)
	}
	if loaded.Header.ID != legacy.Header.ID || opened.Path() != filepath.Join(sessions, "legacy.jsonl") {
		t.Fatalf("fallback opened wrong session: %q", opened.Path())
	}
}
