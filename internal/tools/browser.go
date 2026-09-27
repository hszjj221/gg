package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/browser"
)

// BrowserSessionPool holds one Chromium session shared by the browser tools
// of a single conversation (Service). It is created per defaultTools call,
// so different sessions never share a tab. The session is lazy: Chromium
// starts on the first tool call and lives until Close.
type BrowserSessionPool struct {
	mu sync.Mutex
	s  *browser.Session
}

// NewBrowserSessionPool creates a pool. Call Close when the owning Service
// is done (best-effort; the OS reaps the process on exit regardless).
func NewBrowserSessionPool() *BrowserSessionPool {
	return &BrowserSessionPool{}
}

// Get returns the shared session, starting Chromium on first use.
func (p *BrowserSessionPool) Get(ctx context.Context) (*browser.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.s == nil {
		s, err := browser.Start(ctx)
		if err != nil {
			return nil, err
		}
		p.s = s
	}
	return p.s, nil
}

// Close shuts down the session if one was started.
func (p *BrowserSessionPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.s == nil {
		return nil
	}
	err := p.s.Close()
	p.s = nil
	return err
}

func screenshotDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".gg", "media", "screenshots")
}

// BrowserNavigateTool loads a URL in headless Chromium.
type BrowserNavigateTool struct{ pool *BrowserSessionPool }

func NewBrowserNavigateTool(pool *BrowserSessionPool) BrowserNavigateTool {
	return BrowserNavigateTool{pool: pool}
}

func (t BrowserNavigateTool) Name() string { return "browser_navigate" }

func (t BrowserNavigateTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "browser_navigate",
		Description: "Load a URL in headless Chromium and wait for it to finish loading. Returns the page title.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "The URL to load."},
			},
			"required": []string{"url"},
		},
	}
}

func (t BrowserNavigateTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid browser_navigate arguments: %w", err))
	}
	if !strings.HasPrefix(input.URL, "http://") && !strings.HasPrefix(input.URL, "https://") {
		return errorResult(fmt.Errorf("browser_navigate: only http(s) URLs are allowed"))
	}
	s, err := t.pool.Get(ctx)
	if err != nil {
		return errorResult(err)
	}
	if err := s.Navigate(ctx, input.URL); err != nil {
		return errorResult(err)
	}
	title, err := s.Title(ctx)
	if err != nil {
		return errorResult(err)
	}
	return textResult(fmt.Sprintf("loaded: %s\ntitle: %s", input.URL, title))
}

// BrowserReadTool returns the rendered text of the current page.
type BrowserReadTool struct{ pool *BrowserSessionPool }

func NewBrowserReadTool(pool *BrowserSessionPool) BrowserReadTool {
	return BrowserReadTool{pool: pool}
}

func (t BrowserReadTool) Name() string { return "browser_read" }

func (t BrowserReadTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "browser_read",
		Description: "Read the rendered text of the current browser page (use browser_navigate first).",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t BrowserReadTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	s, err := t.pool.Get(ctx)
	if err != nil {
		return errorResult(err)
	}
	text, err := s.Text(ctx)
	if err != nil {
		return errorResult(err)
	}
	return textResult(truncateRunes(text, 12000))
}

// BrowserScreenshotTool captures the current viewport as PNG.
type BrowserScreenshotTool struct{ pool *BrowserSessionPool }

func NewBrowserScreenshotTool(pool *BrowserSessionPool) BrowserScreenshotTool {
	return BrowserScreenshotTool{pool: pool}
}

func (t BrowserScreenshotTool) Name() string { return "browser_screenshot" }

func (t BrowserScreenshotTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "browser_screenshot",
		Description: "Take a screenshot of the current browser page and save it as PNG. Returns the file path.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t BrowserScreenshotTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	s, err := t.pool.Get(ctx)
	if err != nil {
		return errorResult(err)
	}
	path, err := s.ScreenshotToFile(ctx, screenshotDir())
	if err != nil {
		return errorResult(err)
	}
	return textResult("saved: " + path)
}
