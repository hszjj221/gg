package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/kb"
)

type stubEmbedder struct{ dim int }

func (s stubEmbedder) Model() string { return "stub" }
func (s stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, s.dim)
		for j := 0; j < len(t); j++ {
			v[j%s.dim] += float32(t[j])
		}
		out[i] = v
	}
	return out, nil
}

func buildTestKB(t *testing.T, home string) {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "deploy.md"),
		[]byte("# Deploy\n\nRun `make deploy` to ship. Rollback with `make rollback`."), 0o644)
	emb := stubEmbedder{dim: 8}
	ix, err := kb.BuildIndex(context.Background(), emb, "", root, kb.BuildOptions{})
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	if err := ix.Save(home); err != nil {
		t.Fatalf("save index: %v", err)
	}
}

func TestKBSearchToolExecute(t *testing.T) {
	home := t.TempDir()
	buildTestKB(t, home)
	tool := NewKBSearchTool(home, "", "", KBSearchOptions{Embedder: stubEmbedder{dim: 8}})
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":"how do I deploy"}`))
	if res.IsError {
		t.Fatalf("want success, got error: %v", res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "deploy.md") || !strings.Contains(text, "make deploy") {
		t.Fatalf("want deploy passage, got:\n%s", text)
	}
}

func TestKBSearchToolMissingIndex(t *testing.T) {
	tool := NewKBSearchTool(t.TempDir(), "", "", KBSearchOptions{Embedder: stubEmbedder{dim: 8}})
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":"x"}`))
	if !res.IsError {
		t.Fatal("want error when no index exists")
	}
	if !strings.Contains(res.Content[0].Text, "gg kb index") {
		t.Fatalf("want build hint, got: %s", res.Content[0].Text)
	}
}

func TestKBSearchToolBadArgs(t *testing.T) {
	home := t.TempDir()
	buildTestKB(t, home)
	tool := NewKBSearchTool(home, "", "", KBSearchOptions{Embedder: stubEmbedder{dim: 8}})
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":""}`))
	if !res.IsError {
		t.Fatal("want error for empty query")
	}
	res = tool.Execute(context.Background(), json.RawMessage(`not-json`))
	if !res.IsError {
		t.Fatal("want error for malformed args")
	}
}

func TestKBSearchToolDefinition(t *testing.T) {
	tool := NewKBSearchTool("", "", "", KBSearchOptions{})
	if tool.Name() != "kb_search" {
		t.Fatalf("want name kb_search, got %q", tool.Name())
	}
	def := tool.Definition()
	if def.Name != "kb_search" {
		t.Fatalf("definition name mismatch: %q", def.Name)
	}
}

func TestKBSearchToolEndpointMismatch(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("# A\n\nSome content."), 0o644)
	emb := stubEmbedder{dim: 8}
	ix, err := kb.BuildIndex(context.Background(), emb, "", root,
		kb.BuildOptions{EmbedBaseURL: "https://embed.example.com/v1"})
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	if err := ix.Save(home); err != nil {
		t.Fatalf("save index: %v", err)
	}
	// Tool pointed at a different endpoint: must fail closed, not query it.
	tool := NewKBSearchTool(home, "k", "https://chat.example.com/v1",
		KBSearchOptions{Embedder: stubEmbedder{dim: 8}})
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":"content"}`))
	if !res.IsError {
		t.Fatal("want error on embeddings endpoint mismatch")
	}
	if !strings.Contains(res.Content[0].Text, "GG_EMBED_BASE_URL") {
		t.Fatalf("want mismatch explanation, got: %s", res.Content[0].Text)
	}
}

func TestKBSearchToolDimMismatch(t *testing.T) {
	home := t.TempDir()
	buildTestKB(t, home)
	// Embedder returns the wrong dimension: must surface an error, not
	// silently rank with truncated vectors.
	tool := NewKBSearchTool(home, "", "", KBSearchOptions{Embedder: stubEmbedder{dim: 4}})
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":"how do I deploy"}`))
	if !res.IsError {
		t.Fatal("want error on query vector dimension mismatch")
	}
	if !strings.Contains(res.Content[0].Text, "dim") {
		t.Fatalf("want dim explanation, got: %s", res.Content[0].Text)
	}
}
