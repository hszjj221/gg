package config

import (
	"os"
	"path/filepath"
)

const (
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-4.1"
)

type Options struct {
	APIKey     string
	BaseURL    string
	Model      string
	SessionDir string
	CWD        string
}

type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	SessionDir string
	CWD        string
}

func Resolve(options Options) Config {
	cwd := first(options.CWD, mustGetwd())
	home, _ := os.UserHomeDir()
	defaultSessionDir := filepath.Join(home, ".gg", "sessions")
	return Config{
		APIKey:     first(options.APIKey, os.Getenv("OPENAI_API_KEY")),
		BaseURL:    first(options.BaseURL, os.Getenv("OPENAI_BASE_URL"), DefaultBaseURL),
		Model:      first(options.Model, os.Getenv("GG_MODEL"), DefaultModel),
		SessionDir: first(options.SessionDir, os.Getenv("GG_SESSION_DIR"), defaultSessionDir),
		CWD:        cwd,
	}
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
