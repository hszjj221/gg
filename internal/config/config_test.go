package config

import (
	"os"
	"path/filepath"
	"strings"
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
	if cfg.Context.MaxPromptTokens != 24000 || cfg.Context.TailTurns != 6 || cfg.Context.SummaryMaxTokens != 1200 || !cfg.Context.AutoCompact {
		t.Fatalf("unexpected default context config: %+v", cfg.Context)
	}
	if !cfg.Memory.Enabled || cfg.Memory.MaxPromptTokens != 1200 || cfg.MemoryPath != filepath.Join(home, ".gg", "memory.md") {
		t.Fatalf("unexpected default memory config: %+v path=%q", cfg.Memory, cfg.MemoryPath)
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

func TestResolveReadsContextConfig(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "context": {
    "maxPromptTokens": 1000,
    "tailTurns": 3,
    "summaryMaxTokens": 400,
    "autoCompact": false
  },
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Context.MaxPromptTokens != 1000 || cfg.Context.TailTurns != 3 || cfg.Context.SummaryMaxTokens != 400 || cfg.Context.AutoCompact {
		t.Fatalf("context config not applied: %+v", cfg.Context)
	}
}

func TestResolveReadsMemoryConfigAndCLIDisable(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "memory": {
    "enabled": true,
    "maxPromptTokens": 500
  },
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home, NoMemory: true})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Memory.Enabled || cfg.Memory.MaxPromptTokens != 500 {
		t.Fatalf("memory config not applied: %+v", cfg.Memory)
	}
}

func TestResolveRejectsInvalidMemoryConfig(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "memory": {
    "maxPromptTokens": 0
  },
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)

	_, err := Resolve(Options{HomeDir: home})
	if err == nil || !strings.Contains(err.Error(), "memory.maxPromptTokens") {
		t.Fatalf("expected memory maxPromptTokens error, got %v", err)
	}
}

func TestResolveRejectsInvalidContextConfig(t *testing.T) {
	tests := []struct {
		name    string
		context string
		want    string
	}{
		{name: "max prompt", context: `"maxPromptTokens": 0`, want: "maxPromptTokens"},
		{name: "tail turns", context: `"tailTurns": 0`, want: "tailTurns"},
		{name: "summary max", context: `"summaryMaxTokens": 0`, want: "summaryMaxTokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "context": {`+tt.context+`},
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)

			_, err := Resolve(Options{HomeDir: home})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
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

func TestResolveAPIKeyEnvResolvesKeyFromEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GG_TEST_DEEPSEEK_KEY", "env-secret")
	writeConfig(t, home, `{
  "default": "deepseek:deepseek-chat",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "baseURL": "https://api.deepseek.com",
      "apiKeyEnv": "GG_TEST_DEEPSEEK_KEY",
      "models": ["deepseek-chat"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "env-secret" {
		t.Fatalf("apiKeyEnv was not resolved from environment: %+v", cfg)
	}
}

func TestResolveLiteralAPIKeyWinsOverAPIKeyEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GG_TEST_DEEPSEEK_KEY", "env-secret")
	writeConfig(t, home, `{
  "default": "deepseek:deepseek-chat",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "baseURL": "https://api.deepseek.com",
      "apiKey": "literal-key",
      "apiKeyEnv": "GG_TEST_DEEPSEEK_KEY",
      "models": ["deepseek-chat"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "literal-key" {
		t.Fatalf("literal apiKey did not win over apiKeyEnv: %+v", cfg)
	}
}

func TestResolveCLIAPIKeyWinsOverAPIKeyEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GG_TEST_DEEPSEEK_KEY", "env-secret")
	writeConfig(t, home, `{
  "default": "deepseek:deepseek-chat",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "baseURL": "https://api.deepseek.com",
      "apiKeyEnv": "GG_TEST_DEEPSEEK_KEY",
      "models": ["deepseek-chat"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home, APIKey: "cli-key"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "cli-key" {
		t.Fatalf("CLI --api-key did not win over apiKeyEnv: %+v", cfg)
	}
}

func TestResolveMissingAPIKeyEnvVarLeavesKeyEmpty(t *testing.T) {
	home := t.TempDir()
	os.Unsetenv("GG_TEST_MISSING_KEY")
	writeConfig(t, home, `{
  "default": "deepseek:deepseek-chat",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "baseURL": "https://api.deepseek.com",
      "apiKeyEnv": "GG_TEST_MISSING_KEY",
      "models": ["deepseek-chat"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "" {
		t.Fatalf("unset apiKeyEnv var should leave key empty: %+v", cfg)
	}
}

func TestResolveInfersBaseURLFromWellKnownProviderName(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "deepseek:deepseek-chat",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "apiKeyEnv": "GG_TEST_DEEPSEEK_KEY",
      "models": ["deepseek-chat"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("well-known baseURL was not inferred: %+v", cfg)
	}
}

func TestResolveExplicitBaseURLWinsOverWellKnownInference(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "deepseek:deepseek-chat",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "baseURL": "https://gateway.example/v1",
      "apiKey": "k",
      "models": ["deepseek-chat"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("explicit baseURL did not win over inference: %+v", cfg)
	}
}

func TestResolveUnknownProviderNameFallsBackToDefaultBaseURL(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "acme:acme-1",
  "providers": {
    "acme": {
      "type": "openai-compatible",
      "apiKey": "k",
      "models": ["acme-1"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("unknown provider should fall back to default baseURL: %+v", cfg)
	}
}

func TestResolveCarriesProviderCompat(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "quirky:q-1",
  "providers": {
    "quirky": {
      "type": "openai-compatible",
      "baseURL": "https://quirky.example/v1",
      "apiKey": "k",
      "compat": {"noStreamUsage": true, "completionTokens": true},
      "models": ["q-1"]
    },
    "plain": {
      "type": "openai-compatible",
      "baseURL": "https://plain.example/v1",
      "apiKey": "k",
      "models": ["p-1"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Compat.NoStreamUsage || !cfg.Compat.CompletionTokens {
		t.Fatalf("provider compat was not carried to resolved config: %+v", cfg.Compat)
	}

	plain, err := Resolve(Options{HomeDir: home, Model: "plain:p-1"})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Compat.NoStreamUsage || plain.Compat.CompletionTokens {
		t.Fatalf("compat should default to zero value: %+v", plain.Compat)
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

func TestResolveExpandsMemoryDirTilde(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "memory": {
    "dir": "~/mymem"
  },
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory.Dir != filepath.Join(home, "mymem") {
		t.Fatalf("memory.dir ~/ not expanded: %q", cfg.Memory.Dir)
	}
}
