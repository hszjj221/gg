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

// browserSession is a process-wide lazy Chromium session shared by the
// browser tools. The first tool call starts Chromium; it lives until the
// process exits.
var browserSession struct {
	sync.Mutex
	s *browser.Session
}

func getBrowserSession(ctx context.Context) (*browser.Session, error) {
	browserSession.Lock()
	defer browserSession.Unlock()
	if browserSession.s == nil {
		s, err := browser.Start(ctx)
		if err != nil {
			return nil, err
		}
		browserSession.s = s
	}
	return browserSession.s, nil
}

func screenshotDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".gg", "media", "screenshots")
}

// BrowserNavigateTool loads a URL in headless Chromium.
type BrowserNavigateTool struct{}

func NewBrowserNavigateTool() BrowserNavigateTool { return BrowserNavigateTool{} }

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
	s, err := getBrowserSession(ctx)
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
type BrowserReadTool struct{}

func NewBrowserReadTool() BrowserReadTool { return BrowserReadTool{} }

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
	s, err := getBrowserSession(ctx)
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
type BrowserScreenshotTool struct{}

func NewBrowserScreenshotTool() BrowserScreenshotTool { return BrowserScreenshotTool{} }

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
	s, err := getBrowserSession(ctx)
	if err != nil {
		return errorResult(err)
	}
	path, err := s.ScreenshotToFile(ctx, screenshotDir())
	if err != nil {
		return errorResult(err)
	}
	return textResult("saved: " + path)
}
