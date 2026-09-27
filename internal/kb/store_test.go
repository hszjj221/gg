package kb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func testIndex() *Index {
	chunks := []Chunk{
		{ID: "a#0", Source: "a.md", Text: "the cat sits on the mat"},
		{ID: "b#0", Source: "b.md", Text: "quantum field theory primer"},
		{ID: "c#0", Source: "c.md", Text: "the dog barks at night"},
	}
	vectors := [][]float32{
		{1, 0, 0},
		{0, 1, 0},
		{0.9, 0.1, 0},
	}
	ix, err := NewIndex("test", "m", "", chunks, vectors)
	if err != nil {
		panic(err)
	}
	return ix
}

func TestSearchRanking(t *testing.T) {
	ix := testIndex()
	results, err := ix.Search([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Chunk.Source != "a.md" {
		t.Fatalf("want a.md first, got %s", results[0].Chunk.Source)
	}
	if results[0].Score < 0.99 {
		t.Fatalf("want near-1 score for identical vector, got %f", results[0].Score)
	}
}

func TestSearchTopKClamp(t *testing.T) {
	ix := testIndex()
	got, err := ix.Search([]float32{1, 0, 0}, 99)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want all 3 results, got %d", len(got))
	}
}

func TestSearchDimMismatch(t *testing.T) {
	ix := testIndex()
	if _, err := ix.Search([]float32{1, 0}, 2); err == nil {
		t.Fatal("want error when query dim does not match index dim")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	ix := testIndex()
	if err := ix.Save(home); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !Exists(home, "test") {
		t.Fatal("Exists reports false after save")
	}
	loaded, err := Load(home, "test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Name != "test" || loaded.Model != "m" || loaded.Dim != 3 {
		t.Fatalf("manifest mismatch: %+v", loaded)
	}
	if len(loaded.Chunks) != 3 || loaded.Chunks[1].Text != "quantum field theory primer" {
		t.Fatalf("chunks mismatch: %+v", loaded.Chunks)
	}
}

func TestSaveRestrictivePermissions(t *testing.T) {
	home := t.TempDir()
	ix := testIndex()
	if err := ix.Save(home); err != nil {
		t.Fatalf("save: %v", err)
	}
	dirInfo, err := os.Stat(Dir(home, "test"))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("want index dir 0700, got %o", perm)
	}
	fileInfo, err := os.Stat(IndexPath(home, "test"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("want index file 0600, got %o", perm)
	}
}

func TestNewIndexRecordsEmbedEndpoint(t *testing.T) {
	chunks := []Chunk{{ID: "a", Source: "a", Text: "x"}}
	ix, err := NewIndex("n", "m", "https://embed.example.com/v1/", chunks, [][]float32{{1, 2}})
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	if ix.EmbedBaseURL != "https://embed.example.com/v1" {
		t.Fatalf("want normalized embed endpoint, got %q", ix.EmbedBaseURL)
	}
	// Round-trips through the manifest.
	home := t.TempDir()
	if err := ix.Save(home); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(home, "n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.EmbedBaseURL != ix.EmbedBaseURL {
		t.Fatalf("want %q after round trip, got %q", ix.EmbedBaseURL, loaded.EmbedBaseURL)
	}
}

func TestReadTextFileSanitizesUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.md")
	os.WriteFile(path, []byte("ok \xff\xfe broken \x80 text"), 0o644)
	text, err := readTextFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !utf8.ValidString(text) {
		t.Fatalf("want valid UTF-8, got %q", text)
	}
	if !strings.Contains(text, "ok") || !strings.Contains(text, "text") {
		t.Fatalf("want surrounding text preserved, got %q", text)
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(t.TempDir(), "nope"); err == nil {
		t.Fatal("want error loading missing index")
	}
}

func TestNewIndexDimMismatch(t *testing.T) {
	chunks := []Chunk{{ID: "a", Source: "a", Text: "x"}}
	if _, err := NewIndex("n", "m", "", chunks, [][]float32{{1, 2}, {3}}); err == nil {
		t.Fatal("want error on ragged vectors")
	}
}

func TestBuildIndexWithFakeEmbedder(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Title\n\nSome meaningful content here.\n\nMore content."), 0o644)
	os.WriteFile(filepath.Join(root, "skip.bin"), []byte{0, 1, 2, 3}, 0o644)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("gitdir"), 0o644)

	emb := &fakeEmbedder{model: "fake", dim: 8}
	ix, err := BuildIndex(context.Background(), emb, "demo", root, BuildOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(ix.Chunks) == 0 {
		t.Fatal("want chunks from notes.md")
	}
	for _, c := range ix.Chunks {
		if c.Source == "skip.bin" || c.Source == ".git/config" {
			t.Fatalf("binary or .git file was indexed: %s", c.Source)
		}
	}
	if ix.Model != "fake" || ix.Dim != 8 {
		t.Fatalf("manifest mismatch: model=%s dim=%d", ix.Model, ix.Dim)
	}
}

// fakeEmbedder is a deterministic test embedder: vector[i] = hash-ish of text.
type fakeEmbedder struct {
	model string
	dim   int
}

func (f *fakeEmbedder) Model() string { return f.model }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		v := make([]float32, f.dim)
		for j := 0; j < len(text); j++ {
			v[j%f.dim] += float32(text[j])
		}
		out[i] = v
	}
	return out, nil
}
