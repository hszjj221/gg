package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := openTestStore(t)
	a, err := s.Create("Trip plan", TypeMarkdown, "# Tokyo\n")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.Version != 1 || a.PublishedVersion != 0 {
		t.Fatalf("unexpected artifact: %+v", a)
	}
	got, err := s.Get(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Trip plan" || got.Type != TypeMarkdown {
		t.Fatalf("unexpected get: %+v", got)
	}
	content, err := s.ReadVersion(a.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if content != "# Tokyo\n" {
		t.Fatalf("content = %q", content)
	}
}

func TestCreateValidation(t *testing.T) {
	s := openTestStore(t)
	for name, fn := range map[string]func() error{
		"empty title": func() error { _, err := s.Create("", TypeMarkdown, "x"); return err },
		"bad type":    func() error { _, err := s.Create("t", "pdf", "x"); return err },
		"empty body":  func() error { _, err := s.Create("t", TypeMarkdown, ""); return err },
		"too large": func() error {
			_, err := s.Create("t", TypeMarkdown, strings.Repeat("x", maxContentBytes+1))
			return err
		},
		"unknown id":  func() error { _, err := s.Get("nope"); return err },
		"path escape": func() error { _, err := s.Get("../x"); return err },
		"bad version": func() error { _, err := s.ReadVersion("nope", 1); return err },
	} {
		if err := fn(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := s.Get("../x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("path escape should map to ErrNotFound, got %v", err)
	}
}

func TestVersioning(t *testing.T) {
	s := openTestStore(t)
	a, err := s.Create("Doc", TypeHTML, "<h1>v1</h1>")
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.AddVersion(a.ID, "<h1>v2</h1>")
	if err != nil {
		t.Fatal(err)
	}
	if a.Version != 2 {
		t.Fatalf("version = %d, want 2", a.Version)
	}
	// Old versions are immutable.
	if c, _ := s.ReadVersion(a.ID, 1); c != "<h1>v1</h1>" {
		t.Fatalf("v1 changed: %q", c)
	}
	if c, _ := s.ReadVersion(a.ID, 2); c != "<h1>v2</h1>" {
		t.Fatalf("v2 = %q", c)
	}
	if _, err := s.ReadVersion(a.ID, 3); err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestPublish(t *testing.T) {
	s := openTestStore(t)
	a, err := s.Create("Doc", TypeMarkdown, "# v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddVersion(a.ID, "# v2"); err != nil {
		t.Fatal(err)
	}
	content, ver, err := s.Publish(a.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ver != 2 || content != "# v2" {
		t.Fatalf("publish = v%d %q", ver, content)
	}
	got, _ := s.Get(a.ID)
	if got.PublishedVersion != 2 {
		t.Fatalf("published_version = %d", got.PublishedVersion)
	}
}

func TestPublishVersionChanged(t *testing.T) {
	s := openTestStore(t)
	a, err := s.Create("Doc", TypeMarkdown, "# v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Publish(a.ID, 1); err != nil {
		t.Fatal(err)
	}
	// A concurrent edit lands after the caller read v1: publishing the stale
	// version must fail instead of marking v2 with v1's bytes.
	if _, err := s.AddVersion(a.ID, "# v2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Publish(a.ID, 1); !errors.Is(err, ErrVersionChanged) {
		t.Fatalf("expected ErrVersionChanged, got %v", err)
	}
	got, _ := s.Get(a.ID)
	if got.PublishedVersion != 1 {
		t.Fatalf("published_version changed to %d", got.PublishedVersion)
	}
}

func TestPublishStaleVersionZeroSkipsCheck(t *testing.T) {
	s := openTestStore(t)
	a, _ := s.Create("Doc", TypeMarkdown, "# v1")
	if _, _, err := s.Publish(a.ID, 0); err != nil {
		t.Fatalf("expectedVersion 0 should skip the check, got %v", err)
	}
}

func TestListEmptyReturnsEmptySlice(t *testing.T) {
	s := openTestStore(t)
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("expected non-nil empty list, got %#v", list)
	}
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Fatalf("empty list serializes as %s, want []", data)
	}
}

func TestConcurrentAddVersion(t *testing.T) {
	s := openTestStore(t)
	a, err := s.Create("Doc", TypeMarkdown, "# v1")
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.AddVersion(a.ID, fmt.Sprintf("# v%d", i+2))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Get(a.ID)
	if got.Version != 1+n {
		t.Fatalf("version = %d, want %d", got.Version, 1+n)
	}
	// Every allocated version file must exist exactly once.
	for v := 1; v <= 1+n; v++ {
		if _, err := s.ReadVersion(a.ID, v); err != nil {
			t.Fatalf("version %d unreadable: %v", v, err)
		}
	}
}

func TestListOrderingAndRemove(t *testing.T) {
	s := openTestStore(t)
	a1, _ := s.Create("First", TypeMarkdown, "1")
	a2, _ := s.Create("Second", TypeMarkdown, "2")
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != a2.ID {
		t.Fatalf("list order wrong: %+v", list)
	}
	if err := s.Remove(a1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(a1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed artifact still readable: %v", err)
	}
	if err := s.Remove(a1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double remove should fail: %v", err)
	}
	_ = a2
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Create("Doc", TypeMarkdown, "hi")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		dir:                      0o700,
		filepath.Join(dir, a.ID): 0o700,
		filepath.Join(dir, a.ID, "artifact.json"): 0o600,
		filepath.Join(dir, a.ID, "v1.md"):         0o600,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}
