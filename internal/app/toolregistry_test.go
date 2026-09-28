package app

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
)

func TestToolCapabilitiesDerivedFromRegistry(t *testing.T) {
	got := ToolCapabilities()
	want := []string{"memory", "kb", "artifact", "connector", "media", "browser"}
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

func TestProviderDegradeLogic(t *testing.T) {
	t.Run("memory disabled", func(t *testing.T) {
		tc := testToolContext(t, config.Config{})
		if got := buildMemoryTools(tc); got != nil {
			t.Fatalf("expected nil tools when memory disabled, got %d", len(got))
		}
	})
	t.Run("memory enabled", func(t *testing.T) {
		cfg := config.Config{}
		cfg.Memory.Enabled = true
		if got := buildMemoryTools(testToolContext(t, cfg)); len(got) != 2 {
			t.Fatalf("expected 2 memory tools, got %d", len(got))
		}
	})
	t.Run("kb absent", func(t *testing.T) {
		cfg := config.Config{KBDir: filepath.Join(t.TempDir(), "nope")}
		if got := buildKBTools(testToolContext(t, cfg)); got != nil {
			t.Fatalf("expected nil tools when KB missing, got %d", len(got))
		}
	})
	t.Run("artifact store", func(t *testing.T) {
		cfg := config.Config{}
		cfg.Artifacts.Dir = t.TempDir()
		if got := buildArtifactTools(testToolContext(t, cfg)); len(got) != 2 {
			t.Fatalf("expected 2 artifact tools, got %d", len(got))
		}
	})
	t.Run("connector not connected", func(t *testing.T) {
		cfg := config.Config{}
		cfg.Connectors.Dir = t.TempDir()
		if got := buildConnectorTools(testToolContext(t, cfg)); got != nil {
			t.Fatalf("expected nil tools when Google not connected, got %d", len(got))
		}
	})
	t.Run("media without base URL", func(t *testing.T) {
		if got := buildMediaTools(testToolContext(t, config.Config{})); got != nil {
			t.Fatalf("expected nil tools without media base URL, got %d", len(got))
		}
	})
	t.Run("media client shared across turns", func(t *testing.T) {
		cfg := config.Config{BaseURL: "https://api.example.com", HomeDir: t.TempDir()}
		tc := testToolContext(t, cfg)
		first := buildMediaTools(tc)
		second := buildMediaTools(tc)
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
	tools := s.buildTools(nil)
	if len(tools) == 0 {
		t.Fatal("expected core tools at minimum")
	}
	// A second turn must reuse the memoized resources without panic.
	again := s.buildTools(nil)
	if len(again) != len(tools) {
		t.Fatalf("toolset changed between turns: %d vs %d", len(tools), len(again))
	}
}
