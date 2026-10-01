package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/tools"
)

var errTestProvider = errors.New("test provider failure")

func TestToolCapabilitiesDerivedFromRegistry(t *testing.T) {
	got := ToolCapabilities()
	// The computer capability is only advertised where it has a backend.
	want := []string{"memory", "kb", "artifact", "connector", "media", "browser", "computer", "mcp"}
	if !computerToolsSupported() {
		want = []string{"memory", "kb", "artifact", "connector", "media", "browser", "mcp"}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToolCapabilities() = %v, want %v", got, want)
	}
	// Every named provider must appear exactly once.
	seen := map[string]int{}
	for _, p := range toolProviders {
		if p.Name == "" {
			continue
		}
		seen[p.Name]++
	}
	for _, name := range want {
		if seen[name] != 1 {
			t.Errorf("provider %q registered %d times", name, seen[name])
		}
	}
}

func TestComputerCapabilityAbsentOffSupportedPlatforms(t *testing.T) {
	// The registry must not advertise computer tools where they cannot run:
	// ToolCapabilities is what transports (e.g. system.info) show clients.
	for _, p := range toolProviders {
		if p.Name != "computer" {
			continue
		}
		if p.Available == nil {
			t.Fatal("computer provider needs an Available gate")
		}
		if p.Available() != computerToolsSupported() {
			t.Errorf("Available() = %v, computerToolsSupported() = %v", p.Available(), computerToolsSupported())
		}
	}
}

func testToolContext(t *testing.T, cfg config.Config) ToolContext {
	t.Helper()
	return ToolContext{
		Config:   cfg,
		MemStore: memory.NewStore(t.TempDir()),
		Memoized: &ToolMemo{},
	}
}

// buildForTest calls a provider Build and fails the test on unexpected error.
func buildForTest(t *testing.T, build func(context.Context, ToolContext) ([]agent.Tool, error), tc ToolContext) []agent.Tool {
	t.Helper()
	got, err := build(context.Background(), tc)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return got
}

func TestProviderDegradeLogic(t *testing.T) {
	t.Run("memory disabled", func(t *testing.T) {
		tc := testToolContext(t, config.Config{})
		if got := buildForTest(t, buildMemoryTools, tc); got != nil {
			t.Fatalf("expected nil tools when memory disabled, got %d", len(got))
		}
	})
	t.Run("memory enabled", func(t *testing.T) {
		cfg := config.Config{}
		cfg.Memory.Enabled = true
		if got := buildForTest(t, buildMemoryTools, testToolContext(t, cfg)); len(got) != 2 {
			t.Fatalf("expected 2 memory tools, got %d", len(got))
		}
	})
	t.Run("kb absent", func(t *testing.T) {
		cfg := config.Config{KBDir: filepath.Join(t.TempDir(), "nope")}
		if got := buildForTest(t, buildKBTools, testToolContext(t, cfg)); got != nil {
			t.Fatalf("expected nil tools when KB missing, got %d", len(got))
		}
	})
	t.Run("artifact store", func(t *testing.T) {
		cfg := config.Config{}
		cfg.Artifacts.Dir = t.TempDir()
		if got := buildForTest(t, buildArtifactTools, testToolContext(t, cfg)); len(got) != 2 {
			t.Fatalf("expected 2 artifact tools, got %d", len(got))
		}
	})
	t.Run("connector not connected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.Connectors.Dir = t.TempDir()
		if got := buildForTest(t, buildConnectorTools, testToolContext(t, cfg)); got != nil {
			t.Fatalf("expected nil tools when Google not connected, got %d", len(got))
		}
	})
	t.Run("media without base URL", func(t *testing.T) {
		if got := buildForTest(t, buildMediaTools, testToolContext(t, config.Config{})); got != nil {
			t.Fatalf("expected nil tools without media base URL, got %d", len(got))
		}
	})
	t.Run("media client shared across turns", func(t *testing.T) {
		cfg := config.Config{BaseURL: "https://api.example.com", HomeDir: t.TempDir()}
		tc := testToolContext(t, cfg)
		first := buildForTest(t, buildMediaTools, tc)
		second := buildForTest(t, buildMediaTools, tc)
		if len(first) != 3 || len(second) != 3 {
			t.Fatalf("expected 3 media tools, got %d and %d", len(first), len(second))
		}
		if tc.Memoized.mediaClient == nil {
			t.Fatal("media client was not memoized")
		}
	})
}

func TestToolMemoMediaClientRebuildsOnConfigChange(t *testing.T) {
	m := &ToolMemo{}
	cfg := config.Config{BaseURL: "https://a.example.com", HomeDir: "/tmp/x"}
	c1 := m.MediaClient(cfg)
	c2 := m.MediaClient(cfg)
	if c1 == nil || c1 != c2 {
		t.Fatal("same config must return the same memoized client")
	}
	cfg.BaseURL = "https://b.example.com"
	if c3 := m.MediaClient(cfg); c3 == c1 {
		t.Fatal("changed config must rebuild the media client")
	}
	if m.MediaClient(config.Config{}) != nil {
		t.Fatal("empty base URL must yield nil client")
	}
}

func TestToolMemoChromiumResolvedOnce(t *testing.T) {
	m := &ToolMemo{}
	first := m.ChromiumOK()
	second := m.ChromiumOK()
	if first != second {
		t.Fatal("Chromium probe must be stable across calls")
	}
	if !m.chromiumResolved {
		t.Fatal("probe result was not memoized")
	}
}

func TestBuildToolsDoesNotPanicWithMinimalService(t *testing.T) {
	s := NewService(Options{Config: config.Config{HomeDir: t.TempDir(), CWD: t.TempDir()}})
	// Nil provider mirrors the /context path (definitions only).
	tools := s.buildTools(context.Background(), nil)
	if len(tools) == 0 {
		t.Fatal("expected core tools at minimum")
	}
	// A second turn must reuse the memoized resources without panic.
	again := s.buildTools(context.Background(), nil)
	if len(again) != len(tools) {
		t.Fatalf("toolset changed between turns: %d vs %d", len(tools), len(again))
	}
}

// TestBuildToolsSkipsFailingProvider verifies the degrade-to-absent
// contract: one provider's error must not take down the whole toolset.
func TestBuildToolsSkipsFailingProvider(t *testing.T) {
	s := NewService(Options{Config: config.Config{HomeDir: t.TempDir(), CWD: t.TempDir()}})
	broken := ToolProvider{
		Name: "broken",
		Build: func(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
			return nil, errTestProvider
		},
	}
	toolProviders = append(toolProviders, broken)
	defer func() { toolProviders = toolProviders[:len(toolProviders)-1] }()
	if got := s.buildTools(context.Background(), nil); len(got) == 0 {
		t.Fatal("failing provider must degrade to absent, not empty the toolset")
	}
}

// TestMCPConnectorMemoized ensures one Service reuses a single MCP
// connector (and therefore one set of server subprocesses) across turns.
func TestMCPConnectorMemoized(t *testing.T) {
	m := &ToolMemo{}
	home := t.TempDir()
	if c1, c2 := m.MCPConnector(home), m.MCPConnector(home); c1 == nil || c1 != c2 {
		t.Fatal("same Service must reuse one MCP connector")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close with no servers: %v", err)
	}
	// Close is idempotent.
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestBuildMCPToolsAbsentWithoutConfig verifies the MCP provider degrades
// to absent when ~/.gg/mcp.json does not exist.
func TestBuildMCPToolsAbsentWithoutConfig(t *testing.T) {
	tc := ToolContext{
		Config:   config.Config{HomeDir: t.TempDir()},
		Memoized: &ToolMemo{},
	}
	tools, err := buildMCPTools(context.Background(), tc)
	if err != nil {
		t.Fatalf("missing mcp.json must not error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected no MCP tools, got %d", len(tools))
	}
}

// approvalClassification maps every builtin tool name to whether it must
// implement agent.ApprovalDescriber (i.e. be approval-gated). A tool missing
// from this map fails the sweep test below: adding a tool requires
// explicitly classifying it here, which is what keeps a side-effecting
// tool from silently slipping into main without approval coverage.
var approvalClassification = map[string]bool{
	// Writes and external side effects: every call passes the approval
	// pipeline, and the unattended approver denies them by default.
	"artifact_create": true,
	"artifact_edit":   true,
	"bash":            true,
	"edit":            true,
	"write":           true,
	"image_generate":  true,
	"tts":             true,
	"stt":             true,
	"gmail_send":      true,
	"calendar_create": true,
	"process_kill":    true,
	"open":            true,
	"clipboard_read":  true,
	"clipboard_write": true,
	// Read-only by design: no approval.
	// memory_add writes to the agent's own memory store; it is deliberately
	// not approval-gated (high frequency, user-scoped, no external effect).
	"read": false, "list": false, "grep": false, "subagent": false,
	"memory_add": false, "memory_search": false,
	"kb_search":       false,
	"gmail_read":      false,
	"gmail_search":    false,
	"calendar_agenda": false,
	// Browser navigation is approval-gated: the SSRF check only inspects
	// the initial URL, so the user approves the exact URL Chromium loads.
	// browser_read/screenshot stay exempt: they operate on the page the
	// user already approved, and cannot trigger new network navigation.
	"browser_navigate":   true,
	"browser_read":       false,
	"browser_screenshot": false,
	"computer_info":      false,
	"process_list":       false,
	"notify":             false,
}

// assertApprovalClassified checks one tool against the explicit
// approvalClassification map: the tool must be listed, and its
// ApprovalDescriber implementation must match the classification.
func assertApprovalClassified(t *testing.T, tool agent.Tool) {
	t.Helper()
	name := tool.Name()
	if strings.HasPrefix(name, "mcp_") {
		// Every MCP tool is approval-gated by construction
		// (compile-time assertion in internal/mcp).
		if _, ok := tool.(agent.ApprovalDescriber); !ok {
			t.Errorf("mcp tool %q must implement agent.ApprovalDescriber", name)
		}
		return
	}
	want, ok := approvalClassification[name]
	if !ok {
		t.Errorf("tool %q has no approval classification; add it to approvalClassification", name)
		return
	}
	_, has := tool.(agent.ApprovalDescriber)
	if has != want {
		t.Errorf("tool %q implements ApprovalDescriber = %v, want %v", name, has, want)
	}
}

// TestApprovalInvariantSweep builds every tool the registry can construct
// in this environment and asserts each one is explicitly classified in
// approvalClassification with a matching ApprovalDescriber implementation.
// Providers that degrade to absent here (browser without Chromium, kb
// without an index, connectors without credentials, MCP without servers)
// are covered by TestApprovalInvariantConditionalTools below, which builds
// those tools directly with stubbed prerequisites.
func TestApprovalInvariantSweep(t *testing.T) {
	cfg := config.Config{
		HomeDir: t.TempDir(),
		CWD:     t.TempDir(),
		BaseURL: "https://api.example.com", // enables the media provider
	}
	cfg.Memory.Enabled = true
	cfg.Artifacts.Dir = t.TempDir()
	tc := testToolContext(t, cfg)

	var tools []agent.Tool
	for _, p := range toolProviders {
		if p.Available != nil && !p.Available() {
			continue
		}
		built, err := p.Build(context.Background(), tc)
		if err != nil {
			t.Fatalf("provider %q build failed: %v", p.Name, err)
		}
		tools = append(tools, built...)
	}
	if len(tools) == 0 {
		t.Fatal("sweep built no tools; test would pass vacuously")
	}
	for _, tool := range tools {
		assertApprovalClassified(t, tool)
	}
}

// TestApprovalInvariantConditionalTools covers the tools whose providers
// degrade to absent in a bare test environment, so the sweep above never
// sees them: browser tools (no Chromium), kb_search (no index), and the
// Google connector tools (no credentials). Each tool is constructed
// directly with stubbed prerequisites and run through the same
// classification assertion; a misclassified conditional tool can no
// longer hide behind a missing prerequisite.
func TestApprovalInvariantConditionalTools(t *testing.T) {
	// Browser tools: the pool is lazy, so constructing the tool values
	// never starts Chromium.
	pool := tools.NewBrowserSessionPool()
	for _, tool := range []agent.Tool{
		tools.NewBrowserNavigateTool(pool),
		tools.NewBrowserReadTool(pool),
		tools.NewBrowserScreenshotTool(pool),
	} {
		assertApprovalClassified(t, tool)
	}
	// KB search: construction takes only config values.
	assertApprovalClassified(t,
		tools.NewKBSearchTool(t.TempDir(), "key", "https://api.example.com", tools.KBSearchOptions{}))

	// Connector tools: build a Google client against a stub token store.
	// No HTTP is made; only the tool values are constructed.
	cstore, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cstore.Save(google.Name, connector.Token{
		AccessToken: "at", RefreshToken: "rt",
		Expiry:      time.Now().Add(time.Hour), Scopes: google.Scopes,
	}); err != nil {
		t.Fatal(err)
	}
	gclient, err := google.NewClient(cstore, google.Config{ClientID: "cid"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []agent.Tool{
		tools.NewGmailSearchTool(gclient),
		tools.NewGmailReadTool(gclient),
		tools.NewGmailSendTool(gclient),
		tools.NewCalendarAgendaTool(gclient, nil, nil),
		tools.NewCalendarCreateTool(gclient, nil, nil),
	} {
		assertApprovalClassified(t, tool)
	}
}
