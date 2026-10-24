package app

import (
	"context"
	"path/filepath"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/tools"
)

// mediaTools registers the statically linked media ToolProvider.
// Client memoization and configuration remain host-owned.
func mediaTools() ToolProvider {
	return ToolProvider{Name: "media", Build: buildMediaTools}
}

func buildMediaTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	// Media tools degrade to absent when the provider base URL is missing.
	mclient := tc.Memoized.MediaClient(tc.Config)
	if mclient == nil {
		return nil, nil
	}
	return []agent.Tool{
		tools.NewImageGenerateTool(mclient),
		tools.NewTTSTool(mclient),
		tools.NewSTTToolWithRoots(mclient, []string{
			tc.Config.CWD,
			filepath.Join(tc.Config.HomeDir, ".gg", "media"),
		}),
	}, nil
}
