package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "memory"))
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreEnsureLayoutCreatesTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "memory")
	store := NewStore(dir)
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, filepath.Join(dir, "people"), filepath.Join(dir, "groups"), filepath.Join(dir, "MEMORY.md")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}
	// Second call is a no-op and must not clobber content.
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if string(data) != "keep me" {
		t.Fatalf("EnsureLayout clobbered MEMORY.md: %q", data)
	}
}

func TestStoreMigrateFromFile(t *testing.T) {
	tmp := t.TempDir()
	legacy := filepath.Join(tmp, "memory.md")
	if err := os.WriteFile(legacy, []byte("# gg Memory\n\n- old fact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(tmp, "memory"))
	notice, err := store.MigrateFromFile(legacy)
	if err != nil || notice == "" {
		t.Fatalf("expected migration, got notice=%q err=%v", notice, err)
	}
	data, err := os.ReadFile(store.CuratedPath())
	if err != nil || !strings.Contains(string(data), "old fact") {
		t.Fatalf("curated file missing migrated content: %q err=%v", data, err)
	}
	if _, err := os.Stat(legacy + ".bak"); err != nil {
		t.Fatalf("legacy file should be renamed to .bak: %v", err)
	}
	// Idempotent: nothing left to migrate.
	notice, err = store.MigrateFromFile(legacy)
	if err != nil || notice != "" {
		t.Fatalf("expected no second migration, got notice=%q err=%v", notice, err)
	}
}

func TestStoreMigrateFromFileMissingLegacy(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"))
	notice, err := store.MigrateFromFile(filepath.Join(t.TempDir(), "nope.md"))
	if err != nil || notice != "" {
		t.Fatalf("expected no migration for missing file, got notice=%q err=%v", notice, err)
	}
}

func TestStoreMigrateDoesNotClobberCurated(t *testing.T) {
	tmp := t.TempDir()
	legacy := filepath.Join(tmp, "memory.md")
	if err := os.WriteFile(legacy, []byte("- legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(tmp, "memory"))
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.CuratedPath(), []byte("- existing curated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notice, err := store.MigrateFromFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if notice == "" || !strings.Contains(notice, "not merged") {
		t.Fatalf("expected a manual-merge notice, got %q", notice)
	}
	data, err := os.ReadFile(store.CuratedPath())
	if err != nil || !strings.Contains(string(data), "existing curated") || strings.Contains(string(data), "legacy") {
		t.Fatalf("curated content must be untouched: %q", data)
	}
	if _, err := os.Stat(legacy + ".bak"); err != nil {
		t.Fatalf("legacy file should be retired to .bak: %v", err)
	}
}

func TestStoreAppendDailyAndPerson(t *testing.T) {
	store := newTestStore(t)
	if err := store.AppendDaily("shipped the feature"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.DailyPath(time.Now()))
	if err != nil || !strings.Contains(string(data), "shipped the feature") {
		t.Fatalf("daily log missing entry: %q err=%v", data, err)
	}
	if err := store.AppendPerson("张三", "likes Go"); err != nil {
		t.Fatal(err)
	}
	pdata, err := os.ReadFile(store.PersonPath("张三"))
	if err != nil || !strings.Contains(string(pdata), "likes Go") {
		t.Fatalf("person page missing entry: %q err=%v", pdata, err)
	}
	// Path traversal must not escape the store.
	if err := store.AppendPerson("../../evil", "x"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(store.PersonPath("../../evil"), store.Dir()) {
		t.Fatalf("person path escapes store dir: %s", store.PersonPath("../../evil"))
	}
}

func TestStorePruneDaily(t *testing.T) {
	store := newTestStore(t)
	old := time.Now().AddDate(0, 0, -100).Format("2006-01-02") + ".md"
	recent := time.Now().Format("2006-01-02") + ".md"
	for _, name := range []string{old, recent} {
		if err := os.WriteFile(filepath.Join(store.Dir(), name), []byte("- x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruned, err := store.PruneDaily(90)
	if err != nil || pruned != 1 {
		t.Fatalf("expected 1 pruned, got %d err=%v", pruned, err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), recent)); err != nil {
		t.Fatalf("recent log should survive: %v", err)
	}
	if _, err := os.Stat(store.CuratedPath()); err != nil {
		t.Fatalf("MEMORY.md must never be pruned: %v", err)
	}
	pruned, err = store.PruneDaily(0)
	if err != nil || pruned != 0 {
		t.Fatalf("retention 0 keeps everything, got %d err=%v", pruned, err)
	}
}

func TestStoreLoadDailyTailKeepsRecentLines(t *testing.T) {
	store := newTestStore(t)
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("- 10:00 entry number %02d %s", i, strings.Repeat("x", 20)))
	}
	if err := os.WriteFile(store.DailyPath(time.Now()), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadDailyTail(50)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Exists || !snapshot.Truncated {
		t.Fatalf("expected existing truncated tail, got %+v", snapshot)
	}
	if strings.Contains(snapshot.Content, lines[0]) {
		t.Fatalf("tail should drop the oldest lines")
	}
	if !strings.Contains(snapshot.Content, lines[49]) {
		t.Fatalf("tail should keep the newest lines")
	}
}

func TestStoreSearchScopes(t *testing.T) {
	store := newTestStore(t)
	if err := store.AppendCurated("prefers concise answers"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendDaily("discussed concise writing"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPerson("李四", "concise reviewer"); err != nil {
		t.Fatal(err)
	}

	hits, err := store.Search("concise", "all")
	if err != nil || len(hits) != 3 {
		t.Fatalf("expected 3 hits across scopes, got %d err=%v", len(hits), err)
	}
	hits, err = store.Search("concise", "people")
	if err != nil || len(hits) != 1 || !strings.HasPrefix(hits[0].Path, "people/") {
		t.Fatalf("expected 1 people hit, got %+v err=%v", hits, err)
	}
	hits, err = store.Search("concise", "daily")
	if err != nil || len(hits) != 1 {
		t.Fatalf("expected 1 daily hit, got %+v err=%v", len(hits), err)
	}
	if _, err := store.Search("concise", "bogus"); err == nil {
		t.Fatalf("expected error for unknown scope")
	}
	if _, err := store.Search("   ", "all"); err == nil {
		t.Fatalf("expected error for empty query")
	}
}

func TestStoreSearchRanksByCount(t *testing.T) {
	store := newTestStore(t)
	if err := store.AppendCurated("go go go"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendDaily("go"); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search("go", "all")
	if err != nil || len(hits) < 2 {
		t.Fatalf("expected hits, got %+v err=%v", hits, err)
	}
	if hits[0].Score < hits[1].Score {
		t.Fatalf("expected higher-count hit first: %+v", hits)
	}
}
