package library

import (
	"errors"
	"os"
	"path/filepath"
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

func writeSrc(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAddListRemove(t *testing.T) {
	s := openTestStore(t)
	src := writeSrc(t, "notes.md", "# notes")
	e, err := s.Add(src, "", SourceUpload)
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "notes.md" || e.Source != SourceUpload || e.Size != 7 {
		t.Fatalf("unexpected entry: %+v", e)
	}
	// The library copy must be independent of the source.
	if err := os.WriteFile(src, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(), "notes.md"))
	if err != nil || string(data) != "# notes" {
		t.Fatalf("library copy = %q, err = %v", data, err)
	}
	entries, err := s.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("list = %+v, err = %v", entries, err)
	}
	got, path, err := s.Resolve("notes.md")
	if err != nil || got.ID != e.ID || path != filepath.Join(s.Dir(), "notes.md") {
		t.Fatalf("resolve = %+v %q %v", got, path, err)
	}
	if err := s.Remove(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Resolve(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed entry still resolvable: %v", err)
	}
	if err := s.Remove(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double remove should fail: %v", err)
	}
}

func TestAddDedupesName(t *testing.T) {
	s := openTestStore(t)
	src1 := writeSrc(t, "a.md", "1")
	src2 := writeSrc(t, "b.md", "2")
	e1, err := s.Add(src1, "doc.md", SourceUpload)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.Add(src2, "doc.md", SourceUpload)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Name != "doc.md" || e2.Name != "doc-2.md" {
		t.Fatalf("names = %q, %q", e1.Name, e2.Name)
	}
}

func TestAddValidation(t *testing.T) {
	s := openTestStore(t)
	dir := t.TempDir()
	if _, err := s.Add(filepath.Join(dir, "missing"), "", SourceUpload); err == nil {
		t.Error("missing source should fail")
	}
	if _, err := s.Add(dir, "", SourceUpload); err == nil {
		t.Error("directory source should fail")
	}
	if _, err := s.Add(writeSrc(t, "x", "x"), "../evil", SourceUpload); err != nil {
		// filepath.Base strips the traversal; must not error for that reason,
		// and must not write outside the library dir.
		t.Errorf("base name should be sanitized, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "evil")); err != nil {
		t.Fatalf("sanitized name not stored in dir: %v", err)
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Add(writeSrc(t, "f.md", "x"), "", "artifact:abc")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		dir:                              0o700,
		filepath.Join(dir, "index.json"): 0o600,
		filepath.Join(dir, e.Name):       0o600,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
	if e.Source != "artifact:abc" {
		t.Fatalf("source = %q", e.Source)
	}
}

func TestAddRejectsIndexJSON(t *testing.T) {
	s := openTestStore(t)
	src := writeSrc(t, "index.json", `{"evil": true}`)
	if _, err := s.Add(src, "", SourceUpload); err == nil {
		t.Fatal("expected error adding a file named index.json")
	}
	// Via --name as well, including case variants.
	src2 := writeSrc(t, "data.txt", "data")
	for _, name := range []string{"index.json", "INDEX.JSON", "Index.Json"} {
		if _, err := s.Add(src2, name, SourceUpload); err == nil {
			t.Fatalf("expected error for reserved name %q", name)
		}
	}
	// The real index must be intact and the library still usable.
	if _, err := s.Add(src2, "ok.txt", SourceUpload); err != nil {
		t.Fatalf("library unusable after rejected upload: %v", err)
	}
	entries, err := s.List()
	if err != nil || len(entries) != 1 || entries[0].Name != "ok.txt" {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}
}

func TestDedupeCaseInsensitive(t *testing.T) {
	s := openTestStore(t)
	src1 := writeSrc(t, "Report.md", "# one")
	src2 := writeSrc(t, "report.md", "# two")
	e1, err := s.Add(src1, "", SourceUpload)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.Add(src2, "", SourceUpload)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Name == e2.Name {
		t.Fatalf("case-only names not deduped: %q", e2.Name)
	}
	// Both entries resolve to their own bytes.
	_, p1, err := s.Resolve(e1.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, p2, err := s.Resolve(e2.ID)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	if string(b1) != "# one" || string(b2) != "# two" {
		t.Fatalf("bytes crossed: %q %q", b1, b2)
	}
	// Name lookup is case-insensitive.
	if _, _, err := s.Resolve("REPORT.MD"); err != nil {
		t.Fatalf("case-insensitive resolve failed: %v", err)
	}
}

func TestAddRecordsActualBytes(t *testing.T) {
	s := openTestStore(t)
	src := writeSrc(t, "f.bin", "0123456789")
	e, err := s.Add(src, "", SourceUpload)
	if err != nil {
		t.Fatal(err)
	}
	if e.Size != 10 {
		t.Fatalf("size = %d, want 10", e.Size)
	}
}

func TestConcurrentAdd(t *testing.T) {
	s := openTestStore(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	names := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := writeSrc(t, "same.md", "content")
			e, err := s.Add(src, "", SourceUpload)
			if err != nil {
				errs <- err
				return
			}
			names <- e.Name
			errs <- nil
		}(i)
	}
	wg.Wait()
	close(errs)
	close(names)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for name := range names {
		if seen[name] {
			t.Fatalf("duplicate library name %q", name)
		}
		seen[name] = true
	}
	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		t.Fatalf("entries = %d, want %d (lost update)", len(entries), n)
	}
}
