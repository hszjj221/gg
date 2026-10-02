package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	cfg.Connectors.Dir = t.TempDir()
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
		Expiry: time.Now().Add(time.Hour), Scopes: google.Scopes,
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

// TestBuildToolsRecordsDegradedProviders verifies a failing provider is
// logged and recorded with its reason (not silently swallowed), and that a
// later successful build clears the record.
func TestBuildToolsRecordsDegradedProviders(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	s := NewService(Options{
		Config: config.Config{
			HomeDir:    t.TempDir(),
			CWD:        t.TempDir(),
			Artifacts:  config.ArtifactConfig{Dir: t.TempDir()},
			Connectors: config.ConnectorConfig{Dir: t.TempDir()},
		},
		Log: logger,
	})
	fail := true
	broken := ToolProvider{
		Name: "broken",
		Build: func(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
			if fail {
				return nil, errTestProvider
			}
			return nil, nil
		},
	}
	toolProviders = append(toolProviders, broken)
	defer func() { toolProviders = toolProviders[:len(toolProviders)-1] }()

	s.buildTools(context.Background(), nil)

	degraded := s.DegradedProviders()
	if len(degraded) != 1 || degraded[0].Name != "broken" {
		t.Fatalf("expected [broken] degraded, got %+v", degraded)
	}
	if !strings.Contains(degraded[0].Reason, "test provider failure") {
		t.Fatalf("degraded reason should carry the build error, got %q", degraded[0].Reason)
	}
	if out := logBuf.String(); !strings.Contains(out, "broken") || !strings.Contains(out, "test provider failure") {
		t.Fatalf("expected build failure logged with provider and error, got %q", out)
	}

	// A later successful build clears the record: the provider recovered.
	fail = false
	s.buildTools(context.Background(), nil)
	if got := s.DegradedProviders(); len(got) != 0 {
		t.Fatalf("expected degraded list cleared after recovery, got %+v", got)
	}
}

// TestBuildToolsDegradedUnnamedProviderReportedAsCore covers the defensive
// branch: a failing provider with an empty name is reported as "core" so
// the degraded list never carries a blank entry.
func TestBuildToolsDegradedUnnamedProviderReportedAsCore(t *testing.T) {
	s := NewService(Options{Config: config.Config{
		HomeDir:    t.TempDir(),
		CWD:        t.TempDir(),
		Artifacts:  config.ArtifactConfig{Dir: t.TempDir()},
		Connectors: config.ConnectorConfig{Dir: t.TempDir()},
	}})
	broken := ToolProvider{
		Build: func(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
			return nil, errTestProvider
		},
	}
	toolProviders = append(toolProviders, broken)
	defer func() { toolProviders = toolProviders[:len(toolProviders)-1] }()

	s.buildTools(context.Background(), nil)

	degraded := s.DegradedProviders()
	if len(degraded) != 1 || degraded[0].Name != "core" {
		t.Fatalf("expected unnamed provider reported as core, got %+v", degraded)
	}
}

// TestSanitizeProviderReasonRedactsHomeDir verifies provider build errors
// are scrubbed of the user's home directory before reaching clients: the
// public system.info DTO must not leak local absolute paths (Codex P2 on
// PR #38). The full error still goes to the daemon log.
func TestSanitizeProviderReasonRedactsHomeDir(t *testing.T) {
	reason := sanitizeProviderReason("/home/alice", "read mcp config: open /home/alice/.gg/mcp.json: permission denied")
	if strings.Contains(reason, "/home/alice") {
		t.Fatalf("home dir leaked into client-facing reason: %q", reason)
	}
	if !strings.Contains(reason, "~/.gg/mcp.json") {
		t.Fatalf("reason lost its diagnostic context: %q", reason)
	}
	if got := sanitizeProviderReason("", "plain error"); got != "plain error" {
		t.Fatalf("empty home dir should leave reason untouched, got %q", got)
	}
}

// TestBuildToolsSharedRegistryClearsStaleFailures covers the cross-session
// staleness case (Codex P2 on PR #38): session A records an MCP failure,
// the config is fixed, and session B's successful build clears the entry
// so system.info stops reporting the provider as degraded.
func TestBuildToolsSharedRegistryClearsStaleFailures(t *testing.T) {
	registry := &DegradedRegistry{}
	fail := true
	flaky := ToolProvider{
		Name: "flaky",
		Build: func(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
			if fail {
				return nil, errTestProvider
			}
			return nil, nil
		},
	}
	toolProviders = append(toolProviders, flaky)
	defer func() { toolProviders = toolProviders[:len(toolProviders)-1] }()

	newSvc := func() *Service {
		return NewService(Options{
			Config: config.Config{
				HomeDir:    t.TempDir(),
				CWD:        t.TempDir(),
				Artifacts:  config.ArtifactConfig{Dir: t.TempDir()},
				Connectors: config.ConnectorConfig{Dir: t.TempDir()},
			},
			Degraded: registry,
		})
	}
	s1, s2 := newSvc(), newSvc()

	s1.buildTools(context.Background(), nil)
	if got := registry.List(); len(got) != 1 || got[0].Name != "flaky" {
		t.Fatalf("expected [flaky] degraded after s1 failure, got %+v", got)
	}

	// Provider recovers; only s2 runs a turn. The shared registry must
	// clear the entry even though s1 never ran again.
	fail = false
	s2.buildTools(context.Background(), nil)
	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected stale failure cleared by s2 recovery, got %+v", got)
	}
}

// TestBuildArtifactToolsPropagatesOpenError verifies an artifact store that
// cannot be opened (here: a regular file where the dir should be) is an
// operational failure that reaches the degradation tracker instead of
// degrading silently (Codex P2 on PR #38).
func TestBuildArtifactToolsPropagatesOpenError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{HomeDir: t.TempDir(), CWD: t.TempDir()}
	cfg.Artifacts.Dir = blocker
	if _, err := buildArtifactTools(context.Background(), testToolContext(t, cfg)); err == nil {
		t.Fatal("expected artifact store open failure to propagate")
	}
}

// TestBuildConnectorToolsDistinguishesNotConnected verifies the expected
// disabled state (no token) still degrades silently while a corrupted
// token file is an operational failure that propagates (Codex P2 on
// PR #38).
func TestBuildConnectorToolsDistinguishesNotConnected(t *testing.T) {
	tc := testToolContext(t, config.Config{HomeDir: t.TempDir(), CWD: t.TempDir()})
	tc.Config.Connectors.Dir = t.TempDir()
	// No token file: expected "not connected" state, silent degrade.
	if got, err := buildConnectorTools(context.Background(), tc); err != nil || got != nil {
		t.Fatalf("not-connected should degrade silently, got tools=%v err=%v", got, err)
	}
	// Corrupted token file: operational failure, must propagate.
	tokPath := filepath.Join(tc.Config.Connectors.Dir, "google.json")
	if err := os.WriteFile(tokPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildConnectorTools(context.Background(), tc); err == nil {
		t.Fatal("expected corrupted token file to propagate an error")
	}
}

// TestBuildMCPToolsWarnsOnFailedServer verifies a configured MCP server
// that fails to dial is warned about per server while the provider still
// returns the working servers' tools (no Build error, so nothing is
// dropped). Daemon log gains the signal that used to be silent.
func TestBuildMCPToolsWarnsOnFailedServer(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gg"), 0o700); err != nil {
		t.Fatal(err)
	}
	mcpJSON := `{"servers":{"ghost":{"command":"gg-definitely-not-a-real-binary"}}}`
	if err := os.WriteFile(filepath.Join(home, ".gg", "mcp.json"), []byte(mcpJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	tc := testToolContext(t, config.Config{HomeDir: home, CWD: t.TempDir()})
	tc.Log = slog.New(slog.NewTextHandler(&logBuf, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools, err := buildMCPTools(ctx, tc)
	if err != nil {
		t.Fatalf("partial MCP failure must not fail the provider build: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected no tools from the failed server, got %d", len(tools))
	}
	if out := logBuf.String(); !strings.Contains(out, "ghost") || !strings.Contains(out, "mcp server failed") {
		t.Fatalf("expected per-server failure warning in log, got %q", out)
	}
}

// preApprovalEligible names the only builtin tools allowed to mark an
// ApprovalRequest as pre-approved. The agent-area exemption exists only
// for the agent writing its own files (write/edit targeting
// <workspace>/.gg/agent/); reads, shell commands, and every other
// approval-gated tool must always pass through the approver. Any tool
// that starts setting PreApproved fails the invariant test below until
// it is deliberately allowlisted here — the exemption is a conscious,
// per-tool policy decision, never a drive-by addition.
var preApprovalEligible = map[string]bool{
	"write": true,
	"edit":  true,
}

// preApprovalProbeArgs returns valid ApprovalRequest arguments that
// target the workspace's agent area for the named tool, so the invariant
// test can check whether the tool claims the pre-approval exemption.
// Every approval-gated tool needs an entry here: a tool with no entry
// fails the test loudly, forcing the probe (and the policy decision)
// to be made up front when the tool is added.
func preApprovalProbeArgs(t *testing.T, name, cwd string) json.RawMessage {
	t.Helper()
	switch name {
	case "write":
		return json.RawMessage(`{"path":".gg/agent/probe.txt","content":"probe"}`)
	case "edit":
		// edit only describes existing files.
		agentFile := filepath.Join(cwd, ".gg", "agent", "probe.txt")
		if err := os.MkdirAll(filepath.Dir(agentFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(agentFile, []byte("probe"), 0o600); err != nil {
			t.Fatal(err)
		}
		return json.RawMessage(`{"path":".gg/agent/probe.txt","edits":[{"oldText":"probe","newText":"x"}]}`)
	case "bash":
		// A shell command touching the agent area must still be approved.
		return json.RawMessage(`{"command":"touch .gg/agent/probe.txt"}`)
	case "artifact_create":
		return json.RawMessage(`{"title":"probe","type":"text","content":"probe"}`)
	case "artifact_edit":
		return json.RawMessage(`{"artifact_id":"probe","content":"probe"}`)
	case "open":
		return json.RawMessage(`{"target":".gg/agent/probe.txt"}`)
	case "browser_navigate":
		return json.RawMessage(`{"url":"https://example.com/"}`)
	case "image_generate":
		return json.RawMessage(`{"prompt":"probe"}`)
	case "tts":
		return json.RawMessage(`{"text":"probe"}`)
	case "stt":
		return json.RawMessage(`{"audio_path":".gg/agent/probe.wav"}`)
	case "gmail_send":
		return json.RawMessage(`{"to":"probe@example.com","subject":"probe","body":"probe"}`)
	case "calendar_create":
		return json.RawMessage(`{"title":"probe","start":"2030-01-01T10:00:00+08:00","end":"2030-01-01T11:00:00+08:00"}`)
	case "process_kill":
		return json.RawMessage(`{"pid":987654321}`)
	case "clipboard_read":
		return json.RawMessage(`{}`)
	case "clipboard_write":
		return json.RawMessage(`{"text":"probe"}`)
	default:
		t.Fatalf("no pre-approval probe args for tool %q; add it to preApprovalProbeArgs", name)
		return nil
	}
}

func eligiblePreApprovalTools() string {
	names := make([]string, 0, len(preApprovalEligible))
	for name := range preApprovalEligible {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// TestPreApprovalExemptionInvariant sweeps every approval-gated builtin
// tool — the registry sweep plus the conditional tools that degrade to
// absent in a bare test environment (browser, connectors) — and asserts
// that only the tools in preApprovalEligible ever set
// ApprovalRequest.PreApproved. This is the guardrail against a future
// tool casually opting into the agent-area exemption.
func TestPreApprovalExemptionInvariant(t *testing.T) {
	cfg := config.Config{
		HomeDir: t.TempDir(),
		CWD:     t.TempDir(),
		BaseURL: "https://api.example.com", // enables the media provider
	}
	cfg.Memory.Enabled = true
	cfg.Artifacts.Dir = t.TempDir()
	cfg.Connectors.Dir = t.TempDir()
	tc := testToolContext(t, cfg)

	var all []agent.Tool
	for _, p := range toolProviders {
		if p.Available != nil && !p.Available() {
			continue
		}
		built, err := p.Build(context.Background(), tc)
		if err != nil {
			t.Fatalf("provider %q build failed: %v", p.Name, err)
		}
		all = append(all, built...)
	}
	// Conditional tools that degrade to absent in a bare test environment:
	// the browser pool is lazy so construction never starts Chromium, and
	// the connector tools build against a stub token store with no HTTP.
	pool := tools.NewBrowserSessionPool()
	cstore, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cstore.Save(google.Name, connector.Token{
		AccessToken: "at", RefreshToken: "rt",
		Expiry: time.Now().Add(time.Hour), Scopes: google.Scopes,
	}); err != nil {
		t.Fatal(err)
	}
	gclient, err := google.NewClient(cstore, google.Config{ClientID: "cid"})
	if err != nil {
		t.Fatal(err)
	}
	all = append(all,
		tools.NewBrowserNavigateTool(pool),
		tools.NewGmailSendTool(gclient),
		tools.NewCalendarCreateTool(gclient, nil, nil),
	)
	if len(all) == 0 {
		t.Fatal("sweep built no tools; test would pass vacuously")
	}

	probed := map[string]bool{}
	for _, tool := range all {
		name := tool.Name()
		if strings.HasPrefix(name, "mcp_") {
			// Every MCP tool is approval-gated by construction
			// (compile-time assertion in internal/mcp); none of them
			// set PreApproved.
			continue
		}
		describer, ok := tool.(agent.ApprovalDescriber)
		if !ok {
			continue
		}
		probed[name] = true
		req, err := describer.ApprovalRequest(preApprovalProbeArgs(t, name, cfg.CWD))
		if err != nil {
			t.Fatalf("probe ApprovalRequest for tool %q failed: %v", name, err)
		}
		if want := preApprovalEligible[name]; req.PreApproved != want {
			t.Errorf("tool %q: PreApproved = %v, want %v — only %s may pre-approve",
				name, req.PreApproved, want, eligiblePreApprovalTools())
		}
		if req.PreApproved && req.PreApprovedReason == "" {
			t.Errorf("tool %q set PreApproved without a reason", name)
		}
	}
	// The eligible tools must actually be probed; otherwise the invariant
	// would pass vacuously for them.
	for name := range preApprovalEligible {
		if !probed[name] {
			t.Errorf("pre-approval-eligible tool %q was not probed; extend the sweep", name)
		}
	}
}
