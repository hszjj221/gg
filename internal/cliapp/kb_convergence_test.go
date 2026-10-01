package cliapp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
)

// TestResolveEmbedConfigConvergesWithAgent verifies `gg kb` resolves the
// embeddings key through the same Config.ResolveEmbedKey chain the agent's
// kb_search tool uses: a credentials.json "embed" entry is honored even
// when no flag, env var, or chat key is set (core-runtime.md P1 #1).
func TestResolveEmbedConfigConvergesWithAgent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gg"), 0o755); err != nil {
		t.Fatal(err)
	}
	creds := `{"embed": "file-embed-key"}`
	if err := os.WriteFile(filepath.Join(home, ".gg", "credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GG_EMBED_API_KEY", "")

	cfg := config.Config{HomeDir: home, BaseURL: "https://chat.example.com"}
	ec, err := resolveEmbedConfig(cfg, cli.Args{})
	if err != nil {
		t.Fatalf("resolveEmbedConfig: %v", err)
	}
	if ec.apiKey != "file-embed-key" {
		t.Fatalf("expected credentials.json embed key, got %q", ec.apiKey)
	}

	// Explicit flag still wins over the credentials file.
	ec, err = resolveEmbedConfig(cfg, cli.Args{KBEmbedKey: "flag-key"})
	if err != nil {
		t.Fatalf("resolveEmbedConfig: %v", err)
	}
	if ec.apiKey != "flag-key" {
		t.Fatalf("expected flag key to win, got %q", ec.apiKey)
	}

	// Nothing anywhere: the documented error, not a silent empty key.
	ec, err = resolveEmbedConfig(config.Config{HomeDir: t.TempDir(), BaseURL: "https://chat.example.com"}, cli.Args{})
	if err == nil {
		t.Fatalf("expected missing-key error, got config %+v", ec)
	}
}

// TestEmbedKeyEphemeral verifies the flag-only warning condition: a key
// supplied only via --embed-api-key is ephemeral, while any persistent
// source (credentials file, env, chat key) makes it visible to the agent.
func TestEmbedKeyEphemeral(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gg", "credentials.json"), []byte(`{"embed": "file-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GG_EMBED_API_KEY", "")

	cfg := config.Config{HomeDir: home}
	if !embedKeyEphemeral(config.Config{HomeDir: t.TempDir()}, cli.Args{KBEmbedKey: "flag-key"}) {
		t.Fatal("flag-only key should be ephemeral")
	}
	if embedKeyEphemeral(cfg, cli.Args{KBEmbedKey: "flag-key"}) {
		t.Fatal("credentials file makes the key persistent, not ephemeral")
	}
	if embedKeyEphemeral(cfg, cli.Args{}) {
		t.Fatal("no flag means nothing ephemeral")
	}
}
