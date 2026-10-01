package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CredentialsFileName is the JSON file under ~/.gg holding per-provider
// API keys, e.g. {"deepseek": "sk-..."}. Keep it at 0600: it holds secrets.
// The file is optional; when it is missing or malformed the layer is
// skipped and resolution falls through to the next source.
const CredentialsFileName = "credentials.json"

// AuthResolver resolves a provider's API key from ordered sources:
//
//  1. CLIOverride (--api-key flag)
//  2. APIKey (literal in config.json)
//  3. credentials file (~/.gg/credentials.json)
//  4. APIKeyEnv (named environment variable)
//
// Centralizing the chain here keeps the priority explicit and testable
// instead of scattered across call sites.
type AuthResolver struct {
	CLIOverride string
	// CredentialsPath is the full path to the credentials file;
	// "" disables the credentials-file layer.
	CredentialsPath string
}

// APIKey returns the resolved key, or "" when no source provides one.
func (r AuthResolver) APIKey(providerName string, pc ProviderConfig) string {
	if r.CLIOverride != "" {
		return r.CLIOverride
	}
	if pc.APIKey != "" {
		return pc.APIKey
	}
	if key := credentialsFileKey(r.CredentialsPath, providerName); key != "" {
		return key
	}
	if env := strings.TrimSpace(pc.APIKeyEnv); env != "" {
		return os.Getenv(env)
	}
	return ""
}

// credentialsFileKey reads the key for providerName from a JSON object
// file. Missing or malformed files yield "", disabling the layer.
func credentialsFileKey(path, providerName string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var creds map[string]string
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	return creds[providerName]
}

// EmbedCredentialsName is the credentials.json key holding a dedicated
// embeddings API key, e.g. {"embed": "sk-..."}. It lets the embeddings
// endpoint use a different key than the chat provider without another
// environment variable.
const EmbedCredentialsName = "embed"

// EmbedAPIKeyEnv is the environment variable holding a dedicated
// embeddings API key.
const EmbedAPIKeyEnv = "GG_EMBED_API_KEY"

// ResolveEmbedKey returns the API key for embeddings calls (kb index and
// kb_search). It reuses the AuthResolver chain so the dedicated
// embeddings key enjoys the same layers as chat provider keys:
//
//  1. explicitKey (--embed-api-key flag or a literal config value)
//  2. credentials.json "embed" entry
//  3. GG_EMBED_API_KEY environment variable
//  4. the chat provider's resolved key (itself resolved through the full
//     AuthResolver chain: flag > config > credentials file > apiKeyEnv)
//
// This is the single convergence point: the CLI (`gg kb`) and the agent
// (kb_search tool) must resolve identically, otherwise an index built by
// one cannot be queried by the other.
func (c Config) ResolveEmbedKey(explicitKey string) string {
	if key := (AuthResolver{
		CLIOverride:     explicitKey,
		CredentialsPath: filepath.Join(c.HomeDir, ".gg", CredentialsFileName),
	}.APIKey(EmbedCredentialsName, ProviderConfig{APIKeyEnv: EmbedAPIKeyEnv})); key != "" {
		return key
	}
	return c.APIKey
}
