package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMissingConfigUsesLegacyOpenAIProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_BASE_URL", "https://env.example/v1")
	t.Setenv("GG_MODEL", "ignored-model")

	cfg, err := Resolve(Options{HomeDir: home, CWD: "/tmp/project"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Selection != "openai:gpt-4.1" || cfg.Provider != "openai" || cfg.Model != "gpt-4.1" {
		t.Fatalf("unexpected legacy selection: %+v", cfg)
	}
	if cfg.APIKey != "env-key" || cfg.BaseURL != "https://env.example/v1" {
		t.Fatalf("legacy provider did not use OpenAI environment: %+v", cfg)
	}
	if cfg.CWD != "/tmp/project" || cfg.SessionDir == "" {
		t.Fatalf("missing cwd/session dir: %+v", cfg)
	}
}

func TestResolveReadsProviderConfigAndCLIModelOverride(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1", "gpt-4.1-mini"]
    },
    "local": {
      "type": "openai-compatible",
      "baseURL": "http://localhost:11434/v1",
      "apiKey": "ollama",
      "models": ["qwen2.5-coder"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home, Model: "local:qwen2.5-coder"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Selection != "local:qwen2.5-coder" || cfg.Provider != "local" || cfg.Model != "qwen2.5-coder" {
		t.Fatalf("CLI selection did not win: %+v", cfg)
	}
	if cfg.APIKey != "ollama" || cfg.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("selected provider config not applied: %+v", cfg)
	}
	if got := cfg.AvailableSelections(); len(got) != 3 || got[0] != "local:qwen2.5-coder" || got[1] != "openai:gpt-4.1" || got[2] != "openai:gpt-4.1-mini" {
		t.Fatalf("unexpected selections: %+v", got)
	}
}

func TestResolveCLIAPIKeyAndBaseURLOverrideSelectedProvider(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home, APIKey: "cli-key", BaseURL: "https://cli.example/v1"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.APIKey != "cli-key" || cfg.BaseURL != "https://cli.example/v1" {
		t.Fatalf("CLI provider overrides did not win: %+v", cfg)
	}
}

func TestResolveRejectsMalformedConfig(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{`)

	_, err := Resolve(Options{HomeDir: home})
	if err == nil {
		t.Fatalf("expected malformed config to fail")
	}
}

func TestResolveRejectsUnknownModelWhenProviderHasModelList(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)

	_, err := Resolve(Options{HomeDir: home, Model: "openai:gpt-4.1-mini"})
	if err == nil {
		t.Fatalf("expected unknown model to fail")
	}
}

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".gg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
