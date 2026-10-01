// Package provider builds the agent.Provider for a resolved config.
//
// Construction used to be duplicated across the CLI, the daemon and the
// job runner; the copies drifted apart (one missed the Compat flags).
// There is a single factory now. The provider type was validated during
// config resolution; only openai-compatible exists today, and new
// protocol adapters plug in here.
package provider

import (
	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/provider/openai"
)

// New returns the provider for a resolved config.
func New(cfg config.Config) agent.Provider {
	return openai.NewClient(openai.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		Compat: openai.Compat{
			NoStreamUsage:    cfg.Compat.NoStreamUsage,
			CompletionTokens: cfg.Compat.CompletionTokens,
		},
	})
}
