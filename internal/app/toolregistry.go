package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/browser"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/kb"
	"github.com/hszjj221/gg/internal/mcp"
	"github.com/hszjj221/gg/internal/media"
	"github.com/hszjj221/gg/internal/tools"
)

// ToolMemo caches per-Service tool resources across turns. It is safe for
// concurrent use; callers do not need to hold the owning Service's mutex.
type ToolMemo struct {
	mu               sync.Mutex
	chromiumResolved bool
	chromiumOK       bool
	mediaKey         string
	mediaClient      *media.Client
	mcpConnector     *mcp.Connector
}

// ChromiumOK reports whether browser tools can run. The PATH probe is
// resolved once per Service: a Chromium install doesn't appear or vanish
// mid-conversation in practice, and re-scanning PATH every turn is pure
// waste.
func (m *ToolMemo) ChromiumOK() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.chromiumResolved {
		_, err := browser.FindChromium()
		m.chromiumResolved = true
		m.chromiumOK = err == nil
	}
	return m.chromiumOK
}

// MediaClient returns the shared media client, rebuilt only when its config
// changes. Sharing matters: a fresh http.Client per turn would throw away
// the connection pool and force new TLS handshakes on every image/TTS/STT
// call.
func (m *ToolMemo) MediaClient(cfg config.Config) *media.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	baseURL := firstNonEmpty(cfg.MediaBaseURL, cfg.BaseURL)
	if baseURL == "" {
		m.mediaClient = nil
		m.mediaKey = ""
		return nil
	}
	key := strings.Join([]string{baseURL,
		firstNonEmpty(cfg.MediaAPIKey, cfg.APIKey),
		cfg.MediaImageModel, cfg.MediaTTSModel, cfg.MediaSTTModel,
		cfg.HomeDir,
	}, "\x00")
	mc, ok := cfg.MediaClientConfig()
	if !ok {
		m.mediaClient = nil
		m.mediaKey = ""
		return nil
	}
	if m.mediaClient == nil || m.mediaKey != key {
		m.mediaKey = key
		m.mediaClient = media.NewClient(mc)
	}
	return m.mediaClient
}

// MCPConnector returns the Service-scoped MCP connector, creating it on
// first use. One connector per Service means server subprocesses are
// dialed once per conversation, not once per turn.
func (m *ToolMemo) MCPConnector(homeDir string) *mcp.Connector {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcpConnector == nil {
		m.mcpConnector = mcp.NewConnector(mcp.ConfigPath(homeDir))
	}
	return m.mcpConnector
}

// Close terminates the MCP connector's server sessions. It is idempotent.
func (m *ToolMemo) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcpConnector == nil {
		return nil
	}
	return m.mcpConnector.Close()
}

// toolRegistry registers the built-in providers in tool definition order.
var toolRegistry = NewRegistry(
	ToolProvider{Name: "", Build: buildCoreTools},
	ToolProvider{Name: "memory", Build: buildMemoryTools},
	ToolProvider{Name: "kb", Build: buildKBTools},
	ToolProvider{Name: "artifact", Build: buildArtifactTools},
	ConnectorTools(googleConnector()),
	ToolProvider{Name: "media", Build: buildMediaTools},
	ToolProvider{Name: "browser", Build: buildBrowserTools},
	ToolProvider{Name: "computer", Build: buildComputerTools, Available: computerToolsSupported},
	ToolProvider{Name: "mcp", Build: buildMCPTools},
)

// ToolCapabilities returns the capability identifiers the registry can
// provide on this platform. Transports derive their advertised capabilities
// from this so the list cannot drift from the tools actually registered.
func ToolCapabilities() []string {
	return toolRegistry.Capabilities()
}

// RegistryToolDefinitions builds the full turn toolset for cfg and returns
// the tools' definitions. It exists so tests can size context budgets from
// the real registry instead of a hardcoded tool count: providers come and
// go with the environment (Chromium installed, MCP configured, ...) and the
// registry grows over time, so a fixed budget rots. A nil provider is fine;
// only definitions are needed.
func RegistryToolDefinitions(ctx context.Context, cfg config.Config) []agent.ToolDefinition {
	resources := newConversationTools(Options{Config: cfg})
	defer func() {
		if err := resources.Close(); err != nil {
			resources.logger.Warn("close tool discovery resources", "error", err)
		}
	}()
	return resources.Definitions(ctx, cfg)
}

func buildCoreTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	return []agent.Tool{
		tools.NewReadToolWithOptions(tc.Config.CWD, tools.ReadOptions{ExtraRoots: tc.ReadRoots}),
		tools.NewListTool(tc.Config.CWD),
		tools.NewGrepTool(tc.Config.CWD),
		tools.NewBashTool(tc.Config.CWD, tools.BashOptions{
			Sandbox: tc.Config.BashSandbox,
			Risk: tools.NewCommandRiskJudge(tools.RiskOptions{
				Enabled:  tc.Config.BashRisk.Enabled,
				Endpoint: tc.Config.BashRisk.Endpoint,
				RunBelow: tc.Config.BashRisk.RunBelow,
				AskAbove: tc.Config.BashRisk.AskAbove,
				Timeout:  tc.Config.BashRisk.Timeout,
			}),
		}),
		tools.NewEditTool(tc.Config.CWD),
		tools.NewWriteTool(tc.Config.CWD),
		tools.NewSubagentTool(tc.Config.CWD, tc.Provider, tools.SubagentOptions{}),
	}, nil
}

func buildMemoryTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	if !tc.Config.Memory.Enabled {
		return nil, nil
	}
	return []agent.Tool{
		tools.NewMemoryAddTool(tc.MemStore),
		tools.NewMemorySearchTool(tc.MemStore),
	}, nil
}

func buildKBTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	if !kb.Exists(tc.Config.KBDir, kb.DefaultName) {
		return nil, nil
	}
	// The query embedding must go to the same endpoint the index was
	// built with; prefer dedicated embedding config, fall back to the
	// chat provider. An endpoint mismatch is rejected at call time
	// (fail closed) rather than silently querying the wrong service.
	// The key resolves through Config.ResolveEmbedKey — the same chain
	// `gg kb` uses — so CLI-built indexes are always queryable here.
	embedKey := tc.Config.ResolveEmbedKey("")
	embedBase := tc.Config.EmbedBaseURL
	if embedBase == "" {
		embedBase = tc.Config.BaseURL
	}
	return []agent.Tool{
		tools.NewKBSearchTool(tc.Config.KBDir, embedKey, embedBase, tools.KBSearchOptions{}),
	}, nil
}

func buildArtifactTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	// Artifact.Open creates the store dir when missing; an error here is
	// an operational failure (permissions, read-only fs), so it is
	// propagated to the degradation tracker instead of degrading silently.
	astore, err := artifact.Open(tc.Config.Artifacts.Dir)
	if err != nil {
		return nil, err
	}
	return []agent.Tool{
		tools.NewArtifactCreateTool(astore),
		tools.NewArtifactEditTool(astore),
	}, nil
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

func buildBrowserTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	// Browser tools need a local Chromium; they degrade to a clear error
	// when none is installed. The pool scopes one Chromium session to this
	// Service (one conversation) and is closed via Service.Close when the
	// Service is retired.
	if !tc.Memoized.ChromiumOK() {
		return nil, nil
	}
	return []agent.Tool{
		tools.NewBrowserNavigateTool(tc.BrowserPool),
		tools.NewBrowserReadTool(tc.BrowserPool),
		tools.NewBrowserScreenshotTool(tc.BrowserPool),
	}, nil
}

// computerToolsSupported reports whether the local-computer tools have a
// backend on this platform. Only Linux and macOS do; elsewhere (Windows,
// other Unixes) the whole capability degrades to absent instead of
// advertising tools that always fail.
func computerToolsSupported() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

// buildComputerTools registers the local-computer tools (system info,
// process management, open, notify, clipboard).
func buildComputerTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	if !computerToolsSupported() {
		return nil, nil
	}
	return []agent.Tool{
		tools.NewComputerInfoTool(),
		tools.NewProcessListTool(),
		tools.NewProcessKillTool(),
		tools.NewOpenTool(tc.Config.CWD),
		tools.NewNotifyTool(),
		tools.NewClipboardReadTool(),
		tools.NewClipboardWriteTool(),
	}, nil
}

// buildMCPTools adapts the configured MCP servers' tools. With no
// ~/.gg/mcp.json (or no reachable servers) it degrades to absent. The
// Connector is memoized per Service: servers are dialed once per
// conversation, and stdio subprocesses are reaped on Service.Close.
// Servers that fail to dial or list are warned about individually — their
// tools are missing while the provider as a whole still works, so this is
// logged per server rather than reported as a provider Build failure.
func buildMCPTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	conn := tc.Memoized.MCPConnector(tc.Config.HomeDir)
	tools, err := conn.Tools(ctx)
	if err != nil {
		return nil, err
	}
	log := tc.Log
	if log == nil {
		log = slog.Default()
	}
	for name, ferr := range conn.FailedServers() {
		log.Warn("mcp server failed; its tools are unavailable",
			"server", name, "error", ferr)
	}
	return tools, nil
}
