package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/browser"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
	"github.com/hszjj221/gg/internal/kb"
	"github.com/hszjj221/gg/internal/mcp"
	"github.com/hszjj221/gg/internal/media"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/tools"
)

// ToolProvider contributes one capability's agent tools. Adding a tool means
// adding (or extending) a provider — the central factory below never needs
// to change.
type ToolProvider struct {
	// Name is the capability identifier advertised to clients. Empty means
	// the provider is always on and not advertised (core tools).
	Name string
	// Build returns the capability's tools. A nil slice (or an error) means
	// the capability degrades to absent for this turn — Chromium not
	// installed, Google not connected, MCP server unreachable, ...
	Build func(ctx context.Context, tc ToolContext) ([]agent.Tool, error)
	// Available reports whether the capability can be provided on this
	// platform at all. Nil means always available. A provider that is not
	// available is neither built nor advertised (e.g. computer tools off
	// Linux/macOS).
	Available func() bool
}

// ToolContext carries everything a provider needs to build its tools.
type ToolContext struct {
	Config      config.Config
	Provider    agent.Provider // nil when only definitions are needed (/context)
	ReadRoots   []string
	MemStore    memory.ToolStore
	Location    *time.Location
	LocationErr error
	BrowserPool *tools.BrowserSessionPool
	// Log receives provider-local operational warnings (e.g. one MCP
	// server failing while others work). Never nil in buildTools; tests
	// that build a ToolContext by hand should treat nil as slog.Default().
	Log *slog.Logger
	// Memoized holds per-Service resources so providers don't redo static
	// probes or discard reusable clients every turn.
	Memoized *ToolMemo
}

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
	if m.mediaClient == nil || m.mediaKey != key {
		m.mediaKey = key
		m.mediaClient = media.NewClient(media.Config{
			BaseURL:    baseURL,
			APIKey:     firstNonEmpty(cfg.MediaAPIKey, cfg.APIKey),
			ImageModel: cfg.MediaImageModel,
			TTSModel:   cfg.MediaTTSModel,
			STTModel:   cfg.MediaSTTModel,
			Dir:        filepath.Join(cfg.HomeDir, ".gg", "media"),
		})
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

// toolProviders is the registry. Order determines tool definition order.
var toolProviders = []ToolProvider{
	{Name: "", Build: buildCoreTools},
	{Name: "memory", Build: buildMemoryTools},
	{Name: "kb", Build: buildKBTools},
	{Name: "artifact", Build: buildArtifactTools},
	{Name: "connector", Build: buildConnectorTools},
	{Name: "media", Build: buildMediaTools},
	{Name: "browser", Build: buildBrowserTools},
	{Name: "computer", Build: buildComputerTools, Available: computerToolsSupported},
	{Name: "mcp", Build: buildMCPTools},
}

// ToolCapabilities returns the capability identifiers the registry can
// provide on this platform. Transports derive their advertised capabilities
// from this so the list cannot drift from the tools actually registered.
func ToolCapabilities() []string {
	names := make([]string, 0, len(toolProviders))
	for _, p := range toolProviders {
		if p.Name == "" {
			continue
		}
		if p.Available != nil && !p.Available() {
			continue
		}
		names = append(names, p.Name)
	}
	return names
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
		tools.NewBashTool(tc.Config.CWD, tools.BashOptions{}),
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

func buildConnectorTools(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
	// A connector dir that cannot be opened is an operational failure and
	// is propagated; a missing token is the expected "not connected"
	// state and degrades silently.
	cstore, err := connector.Open(tc.Config.Connectors.Dir)
	if err != nil {
		return nil, err
	}
	tok, err := cstore.Load(google.Name)
	if err != nil {
		if errors.Is(err, connector.ErrNotConnected) {
			return nil, nil
		}
		return nil, err
	}
	gclient, err := google.NewClient(cstore, google.Config{
		ClientID:     tc.Config.Connectors.Google.ClientID,
		ClientSecret: tc.Config.Connectors.Google.ClientSecret,
	})
	if err != nil {
		return nil, nil
	}
	// Register only the tools the user actually granted: a partial
	// (granular-consent) grant must not advertise tools that would just 403.
	var out []agent.Tool
	if hasScope(tok.Scopes, google.ScopeGmailRead) {
		out = append(out,
			tools.NewGmailSearchTool(gclient),
			tools.NewGmailReadTool(gclient),
		)
	}
	if hasScope(tok.Scopes, google.ScopeGmailSend) {
		out = append(out, tools.NewGmailSendTool(gclient))
	}
	if hasScope(tok.Scopes, google.ScopeCalendar) {
		out = append(out,
			tools.NewCalendarAgendaTool(gclient, tc.Location, tc.LocationErr),
			tools.NewCalendarCreateTool(gclient, tc.Location, tc.LocationErr),
		)
	}
	return out, nil
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
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
