package app

import (
	"context"
	"log/slog"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/runlog"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/tools"
	"github.com/hszjj221/gg/internal/userprofile"
)

// conversationTools owns capability construction and all per-conversation
// execution resources. Metadata discovery shares this owner and its cleanup.
type conversationTools struct {
	registry    Registry
	profile     userprofile.Profile
	skillSet    skills.Set
	memStore    memory.StoreAPI
	logger      *slog.Logger
	degraded    *DegradedRegistry
	browserPool *tools.BrowserSessionPool
	toolMemo    *ToolMemo
}

func newConversationTools(options Options) *conversationTools {
	memStore := options.MemoryStore
	if memStore == nil {
		memStore = memory.NewStore(options.Config.Memory.Dir)
	}
	logger := options.Log
	if logger == nil {
		logger = slog.Default()
	}
	degraded := options.Degraded
	if degraded == nil {
		degraded = &DegradedRegistry{}
	}
	registry := toolRegistry
	if options.ToolProviders != nil {
		registry = NewRegistry(options.ToolProviders...)
	}
	return &conversationTools{
		registry: registry,
		profile:  options.Profile, skillSet: options.Skills, memStore: memStore,
		logger: logger, degraded: degraded,
		browserPool: tools.NewBrowserSessionPool(), toolMemo: &ToolMemo{},
	}
}

func (s *conversationTools) Definitions(ctx context.Context, cfg config.Config) []agent.ToolDefinition {
	built := s.Build(ctx, cfg, nil)
	defs := make([]agent.ToolDefinition, 0, len(built))
	for _, tool := range built {
		defs = append(defs, tool.Definition())
	}
	return defs
}

// buildTools assembles the agent toolset for one turn via the provider
// registry. Static resources (Chromium probe, media client) are memoized
// per Service; dynamic state (connector token, model selection, config)
// is re-read every turn so mid-session changes take effect.
func (s *conversationTools) Build(ctx context.Context, cfg config.Config, provider agent.Provider) []agent.Tool {
	logger := runlog.Logger(ctx, s.logger)
	loc, tzErr := userLocation(s.profile)
	tc := ToolContext{
		Config:      cfg,
		Provider:    provider,
		ReadRoots:   s.skillSet.ReadRoots(),
		MemStore:    s.memStore,
		Location:    loc,
		LocationErr: tzErr,
		BrowserPool: s.browserPool,
		Memoized:    s.toolMemo,
		Log:         logger,
	}
	return s.registry.Build(ctx, tc, func(name string, err error) {
		if err != nil {
			// Keep full errors in the log and sanitized reasons in system.info.
			logger.Warn("tool provider build failed; capability degraded to absent",
				"provider", name, "error", err)
			s.degraded.Report(name, sanitizeProviderReason(cfg.HomeDir, err.Error()), err)
			return
		}
		// Success clears failures recorded by this or another session.
		s.degraded.Report(name, "", nil)
	})
}

// Close releases resources held by the Service, including its shared
// Chromium session and MCP connections/subprocesses. It is idempotent and safe to
// call on a Service whose browser tools never started Chromium.
func (s *conversationTools) Close() error {
	// Both closings are best-effort and idempotent; report the first error.
	if err := s.browserPool.Close(); err != nil {
		_ = s.toolMemo.Close()
		return err
	}
	return s.toolMemo.Close()
}
