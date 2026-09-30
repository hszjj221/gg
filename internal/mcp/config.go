package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ServerConfig describes one MCP server. Exactly one of Command and URL
// must be set: Command spawns a stdio subprocess, URL connects over
// streamable HTTP.
type ServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// Config is the ~/.gg/mcp.json file: a named set of MCP servers.
type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

// ConfigPath returns the conventional mcp.json location under homeDir.
func ConfigPath(homeDir string) string {
	return filepath.Join(homeDir, ".gg", "mcp.json")
}

// Load reads path as a Config. A missing file is not an error: it means
// no MCP servers are configured and the provider degrades to absent.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read mcp config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse mcp config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	for name, sc := range c.Servers {
		if name == "" {
			return fmt.Errorf("mcp config: server name must not be empty")
		}
		hasCommand := sc.Command != ""
		hasURL := sc.URL != ""
		if hasCommand == hasURL {
			return fmt.Errorf("mcp config: server %q must set exactly one of command and url", name)
		}
	}
	return nil
}
