package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/memory"
)

func TestMemorySearchToolTagsLayers(t *testing.T) {
	tmp := t.TempDir()
	global := memory.NewStore(filepath.Join(tmp, "global"))
	if err := global.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	overlay := memory.NewOverlay(filepath.Join(tmp, "ws", ".gg", "memory"), global)
	if err := global.AppendCurated("global keyword entry"); err != nil {
		t.Fatal(err)
	}
	if err := overlay.AppendCurated("workspace keyword entry"); err != nil {
		t.Fatal(err)
	}
	tool := NewMemorySearchTool(overlay)

	result := executeTool(t, tool, `{"query":"keyword entry"}`)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "[workspace]") || !strings.Contains(text, "workspace keyword entry") {
		t.Fatalf("missing workspace-tagged hit:\n%s", text)
	}
	if !strings.Contains(text, "[global]") || !strings.Contains(text, "global keyword entry") {
		t.Fatalf("missing global-tagged hit:\n%s", text)
	}
	// Workspace wins the score tie, so it sorts first.
	if strings.Index(text, "[workspace]") > strings.Index(text, "[global]") {
		t.Fatalf("workspace hit must sort before global on equal scores:\n%s", text)
	}
}

func TestMemorySearchToolPlainStoreTagsGlobal(t *testing.T) {
	store := newTestMemoryStore(t)
	if err := store.AppendCurated("plain keyword"); err != nil {
		t.Fatal(err)
	}
	tool := NewMemorySearchTool(store)

	result := executeTool(t, tool, `{"query":"keyword"}`)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "[global]") || strings.Contains(text, "[workspace]") {
		t.Fatalf("plain-store hits must be tagged global only:\n%s", text)
	}
}
