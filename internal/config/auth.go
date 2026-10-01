package config

import (
	"encoding/json"
	"os"
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
