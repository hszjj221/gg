package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestSetWorkspaceIDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	store, err := NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceID("w_0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if got := store.Header().WorkspaceID; got != "w_0123456789abcdef" {
		t.Fatalf("in-memory header WorkspaceID = %q", got)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.WorkspaceID != "w_0123456789abcdef" {
		t.Fatalf("reloaded header WorkspaceID = %q", loaded.Header.WorkspaceID)
	}

	reopened, err := NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Header().WorkspaceID != "w_0123456789abcdef" {
		t.Fatalf("reopened header WorkspaceID = %q", reopened.Header().WorkspaceID)
	}
}

func TestLoadLegacyHeaderWithoutWorkspaceID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.jsonl")
	// v3 header written before the workspaceId field existed.
	raw := `{"type":"session","version":3,"id":"sess1","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.WorkspaceID != "" {
		t.Fatalf("legacy header WorkspaceID = %q, want empty", loaded.Header.WorkspaceID)
	}
	if loaded.Header.Version != CurrentVersion {
		t.Fatalf("legacy header version = %d, want %d", loaded.Header.Version, CurrentVersion)
	}
}

func TestSetWorkspaceIDIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	store, err := NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceID("w_abc"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceID("w_abc"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("second SetWorkspaceID with the same id rewrote the file")
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("file invalid after idempotent SetWorkspaceID: %v", err)
	}
}

func TestSetWorkspaceIDPreservesRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	store, err := NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"one", "two", "three"} {
		if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetWorkspaceID("w_keep"); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.WorkspaceID != "w_keep" {
		t.Fatalf("header WorkspaceID = %q, want w_keep", loaded.Header.WorkspaceID)
	}
	if len(loaded.Entries) != 3 {
		t.Fatalf("expected 3 entries after SetWorkspaceID, got %d", len(loaded.Entries))
	}
	for i, want := range []string{"one", "two", "three"} {
		if loaded.Entries[i].Message.Content != want {
			t.Fatalf("entry %d content = %q, want %q", i, loaded.Entries[i].Message.Content, want)
		}
	}

	// The session must stay appendable after the rewrite.
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "four"}); err != nil {
		t.Fatalf("append after SetWorkspaceID failed: %v", err)
	}
	loaded, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Entries) != 4 || loaded.Entries[3].Message.Content != "four" {
		t.Fatalf("appended message lost after SetWorkspaceID: %+v", loaded.Entries)
	}
}

func TestForkInheritsWorkspaceID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	store, err := NewStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceID("w_fork"); err != nil {
		t.Fatal(err)
	}
	fork, err := store.Fork(nil)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Header().WorkspaceID != "w_fork" {
		t.Fatalf("fork header WorkspaceID = %q, want w_fork", fork.Header().WorkspaceID)
	}
	// Fork of a session without a binding keeps the empty value.
	if err := store.SetWorkspaceID(""); err != nil {
		t.Fatal(err)
	}
	fork2, err := store.Fork(nil)
	if err != nil {
		t.Fatal(err)
	}
	if fork2.Header().WorkspaceID != "" {
		t.Fatalf("fork header WorkspaceID = %q, want empty", fork2.Header().WorkspaceID)
	}
	// The on-disk header must actually carry the field (omitempty writes it
	// only when non-empty).
	raw, err := os.ReadFile(fork.Path())
	if err != nil {
		t.Fatal(err)
	}
	firstLine := strings.SplitN(string(raw), "\n", 2)[0]
	if !strings.Contains(firstLine, `"workspaceId":"w_fork"`) {
		t.Fatalf("fork header line missing workspaceId: %s", firstLine)
	}
}
