package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBrowserNavigateRejectsNonHTTP(t *testing.T) {
	tool := NewBrowserNavigateTool(NewBrowserSessionPool())
	for _, url := range []string{"file:///etc/passwd", "javascript:alert(1)", "ftp://x/y"} {
		raw, _ := json.Marshal(map[string]string{"url": url})
		res := tool.Execute(context.Background(), raw)
		if !res.IsError {
			t.Fatalf("url %q: expected error, got success", url)
		}
	}
}

func TestBrowserToolDefinitions(t *testing.T) {
	tools := []Tool{
		NewBrowserNavigateTool(NewBrowserSessionPool()),
		NewBrowserReadTool(NewBrowserSessionPool()),
		NewBrowserScreenshotTool(NewBrowserSessionPool()),
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		def := tool.Definition()
		if def.Name == "" || def.Description == "" {
			t.Fatalf("tool has empty name/description: %+v", def)
		}
		if seen[def.Name] {
			t.Fatalf("duplicate tool name %q", def.Name)
		}
		seen[def.Name] = true
	}
	for _, want := range []string{"browser_navigate", "browser_read", "browser_screenshot"} {
		if !seen[want] {
			t.Fatalf("missing tool %q", want)
		}
	}
}

func TestBrowserSessionPoolCloseIsIdempotent(t *testing.T) {
	pool := NewBrowserSessionPool()
	// Closing a pool that never started Chromium must be a safe no-op.
	if err := pool.Close(); err != nil {
		t.Fatalf("Close on unused pool: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestBrowserToolsShareServicePool(t *testing.T) {
	// All three browser tools must share the single Service-scoped pool.
	// Separate pools per tool (or per turn) would start one Chromium each.
	pool := NewBrowserSessionPool()
	if got := NewBrowserNavigateTool(pool).pool; got != pool {
		t.Fatal("browser_navigate does not share the Service pool")
	}
	if got := NewBrowserReadTool(pool).pool; got != pool {
		t.Fatal("browser_read does not share the Service pool")
	}
	if got := NewBrowserScreenshotTool(pool).pool; got != pool {
		t.Fatal("browser_screenshot does not share the Service pool")
	}
}
