package config

import "testing"

func TestResolveUsesCLIThenEnvironmentThenDefaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_BASE_URL", "https://env.example/v1")
	t.Setenv("GG_MODEL", "env-model")

	cfg := Resolve(Options{
		APIKey:  "cli-key",
		BaseURL: "https://cli.example/v1",
		Model:   "cli-model",
		CWD:     "/tmp/project",
	})

	if cfg.APIKey != "cli-key" || cfg.BaseURL != "https://cli.example/v1" || cfg.Model != "cli-model" {
		t.Fatalf("CLI values did not win: %+v", cfg)
	}
	if cfg.CWD != "/tmp/project" || cfg.SessionDir == "" {
		t.Fatalf("missing cwd/session dir: %+v", cfg)
	}
}

func TestResolveFallsBackToEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_BASE_URL", "https://env.example/v1")
	t.Setenv("GG_MODEL", "env-model")

	cfg := Resolve(Options{})

	if cfg.APIKey != "env-key" || cfg.BaseURL != "https://env.example/v1" || cfg.Model != "env-model" {
		t.Fatalf("environment fallback failed: %+v", cfg)
	}
}
