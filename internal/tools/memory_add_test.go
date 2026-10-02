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

func TestMemoryAddToolTargetRoutesLayers(t *testing.T) {
	tmp := t.TempDir()
	global := memory.NewStore(filepath.Join(tmp, "global"))
	if err := global.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	overlay := memory.NewOverlay(filepath.Join(tmp, "ws", ".gg", "memory"), global)
	tool := NewMemoryAddTool(overlay)

	// Default target is workspace.
	if result := executeTool(t, tool, `{"content":"project fact"}`); result.IsError {
		t.Fatalf("default target failed: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "ws", ".gg", "memory", "MEMORY.md"))
	if err != nil || !strings.Contains(string(data), "project fact") {
		t.Fatalf("default write must land in workspace layer: %q err=%v", data, err)
	}

	// Explicit workspace target.
	if result := executeTool(t, tool, `{"content":"daily ws","scope":"daily","target":"workspace"}`); result.IsError {
		t.Fatalf("workspace target failed: %+v", result)
	}
	ddata, err := os.ReadFile(filepath.Join(tmp, "ws", ".gg", "memory", time.Now().Format("2006-01-02")+".md"))
	if err != nil || !strings.Contains(string(ddata), "daily ws") {
		t.Fatalf("workspace daily write missing: %q err=%v", ddata, err)
	}

	// Explicit global target lands in the global layer.
	if result := executeTool(t, tool, `{"content":"cross-project pref","target":"global"}`); result.IsError {
		t.Fatalf("global target failed: %+v", result)
	}
	gdata, err := os.ReadFile(global.CuratedPath())
	if err != nil || !strings.Contains(string(gdata), "cross-project pref") {
		t.Fatalf("global write missing: %q err=%v", gdata, err)
	}
	if wdata, _ := os.ReadFile(filepath.Join(tmp, "ws", ".gg", "memory", "MEMORY.md")); strings.Contains(string(wdata), "cross-project pref") {
		t.Fatal("global-targeted write leaked into the workspace layer")
	}

	// Global target works for person/group scopes too.
	if result := executeTool(t, tool, `{"content":"knows Go","scope":"person:Zhang San","target":"global"}`); result.IsError {
		t.Fatalf("global person write failed: %+v", result)
	}
	pdata, err := os.ReadFile(global.PersonPath("Zhang San"))
	if err != nil || !strings.Contains(string(pdata), "knows Go") {
		t.Fatalf("global person page missing entry: %q err=%v", pdata, err)
	}

	// Unknown target is an error.
	if result := executeTool(t, tool, `{"content":"x","target":"bogus"}`); !result.IsError {
		t.Fatalf("expected error for unknown target, got %+v", result)
	}
}

func TestMemoryAddToolTargetIgnoredWithoutWorkspace(t *testing.T) {
	// With a plain store (no workspace context) target is ignored:
	// everything lands in the one store, matching pre-P3 behavior.
	store := newTestMemoryStore(t)
	tool := NewMemoryAddTool(store)

	for _, target := range []string{"workspace", "global", ""} {
		arg := `{"content":"fact for ` + target + `"}`
		if target != "" {
			arg = `{"content":"fact for ` + target + `","target":"` + target + `"}`
		}
		if result := executeTool(t, tool, arg); result.IsError {
			t.Fatalf("target %q failed: %+v", target, result)
		}
	}
	data, err := os.ReadFile(store.CuratedPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"workspace", "global", ""} {
		if !strings.Contains(string(data), "fact for "+target) {
			t.Fatalf("target %q write missing from the plain store:\n%s", target, data)
		}
	}
}

func TestMemoryAddToolDefinitionHasTarget(t *testing.T) {
	def := NewMemoryAddTool(newTestMemoryStore(t)).Definition()

	properties, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing: %+v", def.Parameters)
	}
	target, ok := properties["target"].(map[string]any)
	if !ok {
		t.Fatalf("target property missing: %+v", properties)
	}
	if enum, ok := target["enum"].([]string); !ok || len(enum) != 2 || enum[0] != "workspace" || enum[1] != "global" {
		t.Fatalf("unexpected target enum: %+v", target["enum"])
	}
	for _, want := range []string{"workspace", "global", "When unsure"} {
		if !strings.Contains(def.Description, want) {
			t.Fatalf("description must include layer guidance (%q):\n%s", want, def.Description)
		}
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
