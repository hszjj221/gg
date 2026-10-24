package standingauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testGrant() Grant {
	return Grant{
		Subject: "gg on behalf of alice@example.com",
		Scope: Scope{Tools: []string{"gmail_send"}, Consequences: []string{"E", "U"},
			Arguments: map[string]json.RawMessage{"to": json.RawMessage(`"bob@example.com"`), "as_draft": json.RawMessage(`true`)}},
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		SourceQuote: "For the next hour, gg may create Gmail drafts for bob@example.com on behalf of alice@example.com.",
		Notes:       "drafts only",
	}
}

func TestStoreLifecycle(t *testing.T) {
	dir := DefaultDir(t.TempDir())
	store := New(dir)
	if grants, err := store.List(); err != nil || len(grants) != 0 {
		t.Fatalf("absent list: %v, %v", grants, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("reading absent store created state: %v", err)
	}
	created, err := store.Add(testGrant())
	if err != nil {
		t.Fatal(err)
	}
	if !validID(created.ID) || created.CreatedAt.IsZero() {
		t.Fatalf("missing metadata: %+v", created)
	}
	// Reopening proves persistence rather than an in-memory cache.
	grants, err := New(dir).List()
	if err != nil || len(grants) != 1 || !reflect.DeepEqual(grants[0], created) {
		t.Fatalf("persisted grants: %+v, %v", grants, err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, created.ID+".json"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions %s: %v, %v; want %o", path, info, err, want)
		}
	}
	if err := store.Revoke("../outside"); err == nil {
		t.Fatal("accepted invalid id")
	}
	for range 2 {
		if err := store.Revoke(created.ID); err != nil {
			t.Fatal(err)
		}
	}
	if grants, err := store.List(); err != nil || len(grants) != 0 {
		t.Fatalf("revoked list: %v, %v", grants, err)
	}
}

func TestStoreRejectsIncompleteOrVague(t *testing.T) {
	cases := map[string]func(*Grant){
		"subject":            func(g *Grant) { g.Subject = " " },
		"quote":              func(g *Grant) { g.SourceQuote = "" },
		"expiry":             func(g *Grant) { g.ExpiresAt = time.Time{} },
		"expired":            func(g *Grant) { g.ExpiresAt = time.Now().Add(-time.Second) },
		"tools":              func(g *Grant) { g.Scope.Tools = nil },
		"tool wildcard":      func(g *Grant) { g.Scope.Tools = []string{"gmail_*"} },
		"effects":            func(g *Grant) { g.Scope.Consequences = nil },
		"effect typo":        func(g *Grant) { g.Scope.Consequences = []string{"R"} },
		"goal only":          func(g *Grant) { g.Scope = Scope{} },
		"no resource limits": func(g *Grant) { g.Scope.Arguments = nil },
		"empty limit":        func(g *Grant) { g.Scope.Arguments["to"] = json.RawMessage(`" "`) },
		"null limit":         func(g *Grant) { g.Scope.Arguments["to"] = json.RawMessage(`null`) },
		"vague limit":        func(g *Grant) { g.Scope.Arguments["to"] = json.RawMessage(`"whatever"`) },
	}
	for _, phrase := range []string{"你看着办", "随便", "you figure it out", "do what you think is best", "whatever", "as you like"} {
		cases[phrase] = func(g *Grant) { g.SourceQuote = phrase }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			store := New(filepath.Join(t.TempDir(), "grants"))
			g := testGrant()
			mutate(&g)
			if _, err := store.Add(g); err == nil {
				t.Fatal("invalid grant accepted")
			}
			if grants, err := store.List(); err != nil || len(grants) != 0 {
				t.Fatalf("invalid grant stored: %+v, %v", grants, err)
			}
		})
	}
}

func TestExpiredGrantsRemainListable(t *testing.T) {
	dir := t.TempDir()
	g := testGrant()
	g.ID = "0123456789abcdef0123456789abcdef"
	g.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	g.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, g.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if grants, err := New(dir).List(); err != nil || len(grants) != 1 {
		t.Fatalf("expired grant list: %+v, %v", grants, err)
	}
}
