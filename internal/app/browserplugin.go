package app

import (
	"context"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/tools"
)

// browserTools registers the statically linked browser ToolProvider.
// Chromium probing and the session pool lifetime remain host-owned.
func browserTools() ToolProvider {
	return ToolProvider{Name: "browser", Build: buildBrowserTools}
}

func buildBrowserTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	// Missing Chromium is expected absence, not an operational failure.
	// The pool scopes one Chromium session to this Service (one conversation)
	// and is closed via Service.Close when the Service is retired.
	if !tc.Memoized.ChromiumOK() {
		return nil, nil
	}
	return []agent.Tool{
		tools.NewBrowserNavigateTool(tc.BrowserPool),
		tools.NewBrowserReadTool(tc.BrowserPool),
		tools.NewBrowserScreenshotTool(tc.BrowserPool),
	}, nil
}
