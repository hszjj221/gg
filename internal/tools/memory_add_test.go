package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/memory"
)

func newTestMemoryStore(t *testing.T) *memory.Store {
	t.Helper()
	store := memory.NewStore(filepath.Join(t.TempDir(), "memory"))
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestMemoryAddToolAppendsContent(t *testing.T) {
	store := newTestMemoryStore(t)
	tool := NewMemoryAddTool(store)

	result := executeTool(t, tool, `{"content":"Prefer concise answers."}`)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	if got := result.Content[0].Text; got != "memory added" {
		t.Fatalf("unexpected result: %q", got)
	}
	data, err := os.ReadFile(store.CuratedPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- Prefer concise answers.") {
		t.Fatalf("memory file missing content:\n%s", data)
	}
}

func TestMemoryAddToolRejectsEmptyContent(t *testing.T) {
	tool := NewMemoryAddTool(newTestMemoryStore(t))

	result := executeTool(t, tool, `{"content":"   "}`)
	if !result.IsError || !strings.Contains(result.Content[0].Text, "memory text is required") {
		t.Fatalf("expected empty content error, got %+v", result)
	}
}

func TestMemoryAddToolRoutesScopes(t *testing.T) {
	store := newTestMemoryStore(t)
	tool := NewMemoryAddTool(store)

	if result := executeTool(t, tool, `{"content":"shipped v2","scope":"daily"}`); result.IsError {
		t.Fatalf("daily scope failed: %+v", result)
	}
	data, err := os.ReadFile(store.DailyPath(time.Now()))
	if err != nil || !strings.Contains(string(data), "shipped v2") {
		t.Fatalf("daily log missing entry: %q err=%v", data, err)
	}

	if result := executeTool(t, tool, `{"content":"likes Go","scope":"person:Zhang San"}`); result.IsError {
		t.Fatalf("person scope failed: %+v", result)
	}
	pdata, err := os.ReadFile(store.PersonPath("Zhang San"))
	if err != nil || !strings.Contains(string(pdata), "likes Go") {
		t.Fatalf("person page missing entry: %q err=%v", pdata, err)
	}

	if result := executeTool(t, tool, `{"content":"x","scope":"bogus"}`); !result.IsError {
		t.Fatalf("expected error for unknown scope, got %+v", result)
	}
	if result := executeTool(t, tool, `{"content":"x","scope":"person:"}`); !result.IsError {
		t.Fatalf("expected error for empty person name, got %+v", result)
	}
}

func TestMemoryAddToolDefinitionRequiresContent(t *testing.T) {
	def := NewMemoryAddTool(newTestMemoryStore(t)).Definition()

	required, ok := def.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "content" {
		t.Fatalf("unexpected required schema: %+v", def.Parameters["required"])
	}
	properties, ok := def.Parameters["properties"].(map[string]any)
	if !ok || properties["content"] == nil || properties["scope"] == nil {
		t.Fatalf("content/scope properties missing: %+v", def.Parameters)
	}
	if !strings.Contains(def.Description, "Do not save") {
		t.Fatalf("description should include safety guidance: %q", def.Description)
	}
}

func TestMemoryAddToolDoesNotRequireApproval(t *testing.T) {
	var tool any = NewMemoryAddTool(newTestMemoryStore(t))
	if _, ok := tool.(agent.ApprovalDescriber); ok {
		t.Fatalf("memory_add should not implement ApprovalDescriber")
	}
}

func TestMemorySearchToolFindsMatches(t *testing.T) {
	store := newTestMemoryStore(t)
	if err := store.AppendCurated("prefers concise answers"); err != nil {
		t.Fatal(err)
	}
	tool := NewMemorySearchTool(store)

	result := executeTool(t, tool, `{"query":"concise"}`)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "MEMORY.md") || !strings.Contains(text, "concise") {
		t.Fatalf("unexpected search output: %q", text)
	}

	result = executeTool(t, tool, `{"query":"nothing matches this"}`)
	if result.IsError || result.Content[0].Text != "no memory matches" {
		t.Fatalf("expected no-match message, got %+v", result)
	}

	result = executeTool(t, tool, `{"query":""}`)
	if !result.IsError {
		t.Fatalf("expected error for empty query, got %+v", result)
	}
}

func TestMemorySearchToolDoesNotRequireApproval(t *testing.T) {
	var tool any = NewMemorySearchTool(newTestMemoryStore(t))
	if _, ok := tool.(agent.ApprovalDescriber); ok {
		t.Fatalf("memory_search should not implement ApprovalDescriber")
	}
}
