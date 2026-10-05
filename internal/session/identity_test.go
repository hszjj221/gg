package session

import (
	"path/filepath"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestIdentityFollowsActiveBranchAndClearedName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		id, name := store.Identity()
		loaded := store.State()
		if id != loaded.Header.ID || name != want {
			t.Fatalf("identity = %q, %q; want name %q", id, name, want)
		}
		if loaded.LastInfo != nil && name != loaded.LastInfo.Name {
			t.Fatal("identity disagrees with loaded ancestry")
		}
	}
	check("")
	if err := store.AppendName("original"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "root"}); err != nil {
		t.Fatal(err)
	}
	root := store.LeafID()
	if err := store.AppendName("branch"); err != nil {
		t.Fatal(err)
	}
	check("branch")
	if err := store.Branch(root); err != nil {
		t.Fatal(err)
	}
	check("original")
	if err := store.AppendName(""); err != nil {
		t.Fatal(err)
	}
	check("")
	store, err = NewStore(store.Path(), dir)
	if err != nil {
		t.Fatal(err)
	}
	check("")
}

func TestTreeProjectionDoesNotShareStoredParentIDs(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second"} {
		if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	first := store.TreeEntries()
	*first[1].ParentID = "modified by caller"
	second := store.TreeEntries()
	if second[1].ParentID == nil || *second[1].ParentID != second[0].ID {
		t.Fatal("projection exposed stored parent metadata")
	}
}
