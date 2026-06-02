package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestMemoryAddToolAppendsContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gg", "memory.md")
	tool := NewMemoryAddTool(path)

	result := executeTool(t, tool, `{"content":"Prefer concise answers."}`)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	if got := result.Content[0].Text; got != "memory added" {
		t.Fatalf("unexpected result: %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- Prefer concise answers.") {
		t.Fatalf("memory file missing content:\n%s", data)
	}
}

func TestMemoryAddToolRejectsEmptyContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gg", "memory.md")
	tool := NewMemoryAddTool(path)

	result := executeTool(t, tool, `{"content":"   "}`)
	if !result.IsError || !strings.Contains(result.Content[0].Text, "memory text is required") {
		t.Fatalf("expected empty content error, got %+v", result)
	}
}

func TestMemoryAddToolReturnsWriteErrors(t *testing.T) {
	dir := t.TempDir()
	tool := NewMemoryAddTool(dir)

	result := executeTool(t, tool, `{"content":"Remember this."}`)
	if !result.IsError {
		t.Fatalf("expected write error, got %+v", result)
	}
}

func TestMemoryAddToolDefinitionRequiresContent(t *testing.T) {
	def := NewMemoryAddTool("/tmp/memory.md").Definition()

	required, ok := def.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "content" {
		t.Fatalf("unexpected required schema: %+v", def.Parameters["required"])
	}
	properties, ok := def.Parameters["properties"].(map[string]any)
	if !ok || properties["content"] == nil {
		t.Fatalf("content property missing: %+v", def.Parameters)
	}
	if !strings.Contains(def.Description, "Do not save") {
		t.Fatalf("description should include safety guidance: %q", def.Description)
	}
}

func TestMemoryAddToolDoesNotRequireApproval(t *testing.T) {
	var tool any = NewMemoryAddTool("/tmp/memory.md")
	if _, ok := tool.(agent.ApprovalDescriber); ok {
		t.Fatalf("memory_add should not implement ApprovalDescriber")
	}
}
