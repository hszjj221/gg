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
