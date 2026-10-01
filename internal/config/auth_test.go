package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCredentials(t *testing.T, home, content string) string {
	t.Helper()
	dir := filepath.Join(home, ".gg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, CredentialsFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAuthResolverPriorityOrder(t *testing.T) {
	home := t.TempDir()
	credPath := writeCredentials(t, home, `{"deepseek": "file-key"}`)
	t.Setenv("GG_TEST_AUTH_KEY", "env-key")

	pc := ProviderConfig{APIKey: "literal-key", APIKeyEnv: "GG_TEST_AUTH_KEY"}
	r := AuthResolver{CLIOverride: "cli-key", CredentialsPath: credPath}
	if got := r.APIKey("deepseek", pc); got != "cli-key" {
		t.Fatalf("CLI override should win: %q", got)
	}

	r.CLIOverride = ""
	if got := r.APIKey("deepseek", pc); got != "literal-key" {
		t.Fatalf("literal apiKey should beat credentials file: %q", got)
	}

	pc.APIKey = ""
	if got := r.APIKey("deepseek", pc); got != "file-key" {
		t.Fatalf("credentials file should beat apiKeyEnv: %q", got)
	}

	if got := r.APIKey("other", pc); got != "env-key" {
		t.Fatalf("apiKeyEnv should be the last resort: %q", got)
	}

	os.Unsetenv("GG_TEST_AUTH_KEY")
	if got := r.APIKey("other", pc); got != "" {
		t.Fatalf("no source should yield empty key: %q", got)
	}
}

func TestAuthResolverSkipsMissingOrMalformedCredentialsFile(t *testing.T) {
	home := t.TempDir()
	r := AuthResolver{CredentialsPath: filepath.Join(home, ".gg", CredentialsFileName)}
	if got := r.APIKey("deepseek", ProviderConfig{}); got != "" {
		t.Fatalf("missing credentials file should be skipped: %q", got)
	}

	writeCredentials(t, home, `{broken`)
	if got := r.APIKey("deepseek", ProviderConfig{}); got != "" {
		t.Fatalf("malformed credentials file should be skipped: %q", got)
	}

	r.CredentialsPath = ""
	writeCredentials(t, home, `{"deepseek": "file-key"}`)
	if got := r.APIKey("deepseek", ProviderConfig{}); got != "" {
		t.Fatalf("empty credentials path should disable the layer: %q", got)
	}
}

func TestResolveReadsKeyFromCredentialsFile(t *testing.T) {
	home := t.TempDir()
	writeCredentials(t, home, `{"deepseek": "file-key"}`)
	writeConfig(t, home, `{
  "default": "deepseek:deepseek-chat",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "models": ["deepseek-chat"]
    }
  }
}`)

	cfg, err := Resolve(Options{HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "file-key" {
		t.Fatalf("credentials file key was not used: %+v", cfg)
	}
	if cfg.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("baseURL inference should still apply: %+v", cfg)
	}
}

// TestResolveEmbedKeyPriority verifies the single convergence point for
// embeddings auth: explicit > credentials.json "embed" > GG_EMBED_API_KEY
// > chat provider key (core-runtime.md P1 #1 — the dedicated key used to
// bypass the credentials-file and apiKeyEnv layers entirely).
func TestResolveEmbedKeyPriority(t *testing.T) {
	home := t.TempDir()
	writeCredentials(t, home, `{"embed": "file-embed-key"}`)
	t.Setenv(EmbedAPIKeyEnv, "env-embed-key")

	cfg := Config{HomeDir: home, APIKey: "chat-key"}

	if got := cfg.ResolveEmbedKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("explicit key should win: %q", got)
	}
	if got := cfg.ResolveEmbedKey(""); got != "file-embed-key" {
		t.Fatalf("credentials.json embed entry should beat env: %q", got)
	}

	// No credentials file: env var is next.
	home2 := t.TempDir()
	cfg2 := Config{HomeDir: home2, APIKey: "chat-key"}
	if got := cfg2.ResolveEmbedKey(""); got != "env-embed-key" {
		t.Fatalf("GG_EMBED_API_KEY should be used without credentials file: %q", got)
	}

	// Nothing dedicated: fall back to the chat provider key.
	t.Setenv(EmbedAPIKeyEnv, "")
	if got := cfg2.ResolveEmbedKey(""); got != "chat-key" {
		t.Fatalf("should fall back to chat provider key: %q", got)
	}
	if got := (Config{HomeDir: home2}).ResolveEmbedKey(""); got != "" {
		t.Fatalf("no key anywhere should yield empty: %q", got)
	}
}

// TestResolveEmbedKeySkipsMalformedCredentialsFile verifies a broken
// credentials file doesn't break embed resolution; it falls through.
func TestResolveEmbedKeySkipsMalformedCredentialsFile(t *testing.T) {
	home := t.TempDir()
	writeCredentials(t, home, `{not json`)
	t.Setenv(EmbedAPIKeyEnv, "env-embed-key")
	cfg := Config{HomeDir: home, APIKey: "chat-key"}
	if got := cfg.ResolveEmbedKey(""); got != "env-embed-key" {
		t.Fatalf("malformed credentials file should fall through to env: %q", got)
	}
}
