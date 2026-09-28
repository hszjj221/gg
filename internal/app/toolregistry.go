package app

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/browser"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
	"github.com/hszjj221/gg/internal/kb"
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
	// Build returns the capability's tools, or nil when it degrades to
	// absent (Chromium not installed, Google not connected, ...).
	Build func(tc ToolContext) []agent.Tool
}

// ToolContext carries everything a provider needs to build its tools.
type ToolContext struct {
	Config      config.Config
	Provider    agent.Provider // nil when only definitions are needed (/context)
	ReadRoots   []string
	MemStore    *memory.Store
	Location    *time.Location
	LocationErr error
	BrowserPool *tools.BrowserSessionPool
	// Memoized holds per-Service resources so providers don't redo static
	// probes or discard reusable clients every turn.
	Memoized *ToolMemo
}

// ToolMemo caches per-Service tool resources across turns. All methods must
// be called with the owning Service's mutex held.
type ToolMemo struct {
	chromiumResolved bool
	chromiumOK       bool
	mediaKey         string
	mediaClient      *media.Client
}

// ChromiumOK reports whether browser tools can run. The PATH probe is
// resolved once per Service: a Chromium install doesn't appear or vanish
// mid-conversation in practice, and re-scanning PATH every turn is pure
// waste.
func (m *ToolMemo) ChromiumOK() bool {
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

// toolProviders is the registry. Order determines tool definition order.
var toolProviders = []ToolProvider{
	{Name: "", Build: buildCoreTools},
	{Name: "memory", Build: buildMemoryTools},
	{Name: "kb", Build: buildKBTools},
	{Name: "artifact", Build: buildArtifactTools},
	{Name: "connector", Build: buildConnectorTools},
	{Name: "media", Build: buildMediaTools},
	{Name: "browser", Build: buildBrowserTools},
}

// ToolCapabilities returns the capability identifiers the registry can
// provide. Transports derive their advertised capabilities from this so the
// list cannot drift from the tools actually registered.
func ToolCapabilities() []string {
	names := make([]string, 0, len(toolProviders))
	for _, p := range toolProviders {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return names
}

func buildCoreTools(tc ToolContext) []agent.Tool {
	return []agent.Tool{
		tools.NewReadToolWithOptions(tc.Config.CWD, tools.ReadOptions{ExtraRoots: tc.ReadRoots}),
		tools.NewListTool(tc.Config.CWD),
		tools.NewGrepTool(tc.Config.CWD),
		tools.NewBashTool(tc.Config.CWD, tools.BashOptions{}),
		tools.NewEditTool(tc.Config.CWD),
		tools.NewWriteTool(tc.Config.CWD),
		tools.NewSubagentTool(tc.Config.CWD, tc.Provider, tools.SubagentOptions{}),
	}
}

func buildMemoryTools(tc ToolContext) []agent.Tool {
	if !tc.Config.Memory.Enabled {
		return nil
	}
	return []agent.Tool{
		tools.NewMemoryAddTool(tc.MemStore),
		tools.NewMemorySearchTool(tc.MemStore),
	}
}

func buildKBTools(tc ToolContext) []agent.Tool {
	if !kb.Exists(tc.Config.KBDir, kb.DefaultName) {
		return nil
	}
	// The query embedding must go to the same endpoint the index was
	// built with; prefer dedicated embedding config, fall back to the
	// chat provider. An endpoint mismatch is rejected at call time
	// (fail closed) rather than silently querying the wrong service.
	embedKey := tc.Config.EmbedAPIKey
	if embedKey == "" {
		embedKey = tc.Config.APIKey
	}
	embedBase := tc.Config.EmbedBaseURL
	if embedBase == "" {
		embedBase = tc.Config.BaseURL
	}
	return []agent.Tool{
		tools.NewKBSearchTool(tc.Config.KBDir, embedKey, embedBase, tools.KBSearchOptions{}),
	}
}

func buildArtifactTools(tc ToolContext) []agent.Tool {
	// Artifact tools degrade to absent when the store cannot be opened;
	// everything else keeps working.
	astore, err := artifact.Open(tc.Config.Artifacts.Dir)
	if err != nil {
		return nil
	}
	return []agent.Tool{
		tools.NewArtifactCreateTool(astore),
		tools.NewArtifactEditTool(astore),
	}
}

func buildConnectorTools(tc ToolContext) []agent.Tool {
	// Connector tools degrade to absent when Google is not connected.
	cstore, err := connector.Open(tc.Config.Connectors.Dir)
	if err != nil {
		return nil
	}
	tok, err := cstore.Load(google.Name)
	if err != nil {
		return nil
	}
	gclient, err := google.NewClient(cstore, google.Config{
		ClientID:     tc.Config.Connectors.Google.ClientID,
		ClientSecret: tc.Config.Connectors.Google.ClientSecret,
	})
	if err != nil {
		return nil
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
	return out
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

func buildMediaTools(tc ToolContext) []agent.Tool {
	// Media tools degrade to absent when the provider base URL is missing.
	mclient := tc.Memoized.MediaClient(tc.Config)
	if mclient == nil {
		return nil
	}
	return []agent.Tool{
		tools.NewImageGenerateTool(mclient),
		tools.NewTTSTool(mclient),
		tools.NewSTTToolWithRoots(mclient, []string{
			tc.Config.CWD,
			filepath.Join(tc.Config.HomeDir, ".gg", "media"),
		}),
	}
}

func buildBrowserTools(tc ToolContext) []agent.Tool {
	// Browser tools need a local Chromium; they degrade to a clear error
	// when none is installed. The pool scopes one Chromium session to this
	// Service (one conversation) and is closed via Service.Close when the
	// Service is retired.
	if !tc.Memoized.ChromiumOK() {
		return nil
	}
	return []agent.Tool{
		tools.NewBrowserNavigateTool(tc.BrowserPool),
		tools.NewBrowserReadTool(tc.BrowserPool),
		tools.NewBrowserScreenshotTool(tc.BrowserPool),
	}
}
