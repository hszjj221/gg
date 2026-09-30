package app

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
)

var errTestProvider = errors.New("test provider failure")

func TestToolCapabilitiesDerivedFromRegistry(t *testing.T) {
	got := ToolCapabilities()
	want := []string{"memory", "kb", "artifact", "connector", "media", "browser", "mcp"}
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
