package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindByID(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	w1, err := r.Add("one", t.TempDir())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := r.Add("two", t.TempDir()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, ok := r.FindByID(w1.ID)
	if !ok || got.Name != "one" {
		t.Fatalf("FindByID(%q) = %+v, %v", w1.ID, got, ok)
	}
	if _, ok := r.FindByID("w_doesnotexist"); ok {
		t.Fatal("FindByID on unknown id returned a workspace")
	}
	if _, ok := r.FindByID(""); ok {
		t.Fatal("FindByID on empty id returned a workspace")
	}
}

func TestResolveForSessionByIDNoBackfill(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	proj, err := r.Add("proj", t.TempDir())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	w, backfill, err := r.ResolveForSession(proj.ID, "")
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if w.ID != proj.ID {
		t.Fatalf("resolved workspace = %q, want %q", w.ID, proj.ID)
	}
	if backfill {
		t.Fatal("backfill = true for an ID hit, want false")
	}
}

func TestResolveForSessionByRootNeedsBackfill(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	root := t.TempDir()
	proj, err := r.Add("proj", root)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	w, backfill, err := r.ResolveForSession("", root)
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if w.ID != proj.ID {
		t.Fatalf("resolved workspace = %q, want %q", w.ID, proj.ID)
	}
	if !backfill {
		t.Fatal("backfill = false for a root match, want true")
	}
}

func TestResolveForSessionUnknownIDRebindsByRoot(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	root := t.TempDir()
	proj, err := r.Add("proj", root)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A workspace registered under a different ID (e.g. re-registered
	// after the registry was recreated): the stale session ID must fall
	// back to the root match.
	w, backfill, err := r.ResolveForSession("w_stale0000000000", root)
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if w.ID != proj.ID {
		t.Fatalf("resolved workspace = %q, want %q", w.ID, proj.ID)
	}
	if !backfill {
		t.Fatal("backfill = false for unknown-ID root rebind, want true")
	}
}

func TestResolveForSessionUnregisteredRootFallsBackToDefault(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def, err := r.Add(DefaultName, t.TempDir())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	w, backfill, err := r.ResolveForSession("", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if w.ID != def.ID {
		t.Fatalf("resolved workspace = %q, want default %q", w.ID, def.ID)
	}
	if !backfill {
		t.Fatal("backfill = false for default fallback, want true")
	}
}

func TestResolveForSessionMissingCwdFallsBackToDefault(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def, err := r.Add(DefaultName, t.TempDir())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The cwd no longer exists: canonicalRoot errors, so the error must not
	// escape and the default workspace must win.
	missing := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}
	w, backfill, err := r.ResolveForSession("", missing)
	if err != nil {
		t.Fatalf("ResolveForSession: %v", err)
	}
	if w.ID != def.ID {
		t.Fatalf("resolved workspace = %q, want default %q", w.ID, def.ID)
	}
	if !backfill {
		t.Fatal("backfill = false for missing-cwd fallback, want true")
	}
}

func TestResolveForSessionWithoutDefaultErrors(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, _, err := r.ResolveForSession("", t.TempDir()); err == nil {
		t.Fatal("ResolveForSession with no registered workspace returned no error")
	}
	if _, _, err := r.ResolveForSession("w_unknown", ""); err == nil {
		t.Fatal("ResolveForSession with no registered workspace returned no error")
	}
}
