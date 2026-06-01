package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DefaultProvider  = "openai"
	DefaultBaseURL   = "https://api.openai.com/v1"
	DefaultModel     = "gpt-4.1"
	DefaultSelection = DefaultProvider + ":" + DefaultModel

	DefaultMaxPromptTokens  = 24000
	DefaultTailTurns        = 6
	DefaultSummaryMaxTokens = 1200

	ProviderTypeOpenAICompatible = "openai-compatible"
)

type Options struct {
	APIKey     string
	BaseURL    string
	Model      string
	SessionDir string
	CWD        string
	HomeDir    string
}

type ProviderConfig struct {
	Type    string   `json:"type"`
	BaseURL string   `json:"baseURL"`
	APIKey  string   `json:"apiKey"`
	Models  []string `json:"models"`
}

type ContextConfig struct {
	MaxPromptTokens  int  `json:"maxPromptTokens"`
	TailTurns        int  `json:"tailTurns"`
	SummaryMaxTokens int  `json:"summaryMaxTokens"`
	AutoCompact      bool `json:"autoCompact"`
}

type Config struct {
	APIKey       string
	BaseURL      string
	Model        string
	Provider     string
	ProviderType string
	Selection    string
	Providers    map[string]ProviderConfig
	Context      ContextConfig
	SessionDir   string
	CWD          string

	apiKeyOverride  string
	baseURLOverride string
}

type fileConfig struct {
	Default   string                    `json:"default"`
	Providers map[string]ProviderConfig `json:"providers"`
	Context   contextFileConfig         `json:"context"`
}

type contextFileConfig struct {
	MaxPromptTokens  *int  `json:"maxPromptTokens"`
	TailTurns        *int  `json:"tailTurns"`
	SummaryMaxTokens *int  `json:"summaryMaxTokens"`
	AutoCompact      *bool `json:"autoCompact"`
}

func Resolve(options Options) (Config, error) {
	cwd := first(options.CWD, mustGetwd())
	home := resolveHomeDir(options.HomeDir)
	defaultSessionDir := filepath.Join(home, ".gg", "sessions")
	sessionDir := first(options.SessionDir, os.Getenv("GG_SESSION_DIR"), defaultSessionDir)

	cfgFile, found, err := loadFileConfig(filepath.Join(home, ".gg", "config.json"))
	if err != nil {
		return Config{}, err
	}
	if !found {
		cfgFile = legacyConfig()
	}
	if cfgFile.Default == "" {
		cfgFile.Default = DefaultSelection
	}
	if err := validateProviders(cfgFile.Providers); err != nil {
		return Config{}, err
	}
	contextConfig, err := resolveContextConfig(cfgFile.Context)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Providers:       cfgFile.Providers,
		Context:         contextConfig,
		SessionDir:      sessionDir,
		CWD:             cwd,
		apiKeyOverride:  options.APIKey,
		baseURLOverride: options.BaseURL,
	}
	return cfg.WithSelection(first(options.Model, cfgFile.Default))
}

func (c Config) WithSelection(selection string) (Config, error) {
	providerName, model, err := ParseSelection(selection)
	if err != nil {
		return Config{}, err
	}
	provider, ok := c.Providers[providerName]
	if !ok {
		return Config{}, fmt.Errorf("unknown provider %q", providerName)
	}
	if provider.Type == "" {
		provider.Type = ProviderTypeOpenAICompatible
	}
	if provider.Type != ProviderTypeOpenAICompatible {
		return Config{}, fmt.Errorf("unsupported provider %q type %q", providerName, provider.Type)
	}
	if len(provider.Models) > 0 && !contains(provider.Models, model) {
		return Config{}, fmt.Errorf("unknown model %q for provider %q", model, providerName)
	}

	next := c
	next.Provider = providerName
	next.Model = model
	next.Selection = providerName + ":" + model
	next.ProviderType = provider.Type
	next.BaseURL = first(next.baseURLOverride, provider.BaseURL, DefaultBaseURL)
	next.APIKey = first(next.apiKeyOverride, provider.APIKey)
	return next, nil
}

func (c Config) AvailableSelections() []string {
	var out []string
	for providerName, provider := range c.Providers {
		for _, model := range provider.Models {
			out = append(out, providerName+":"+model)
		}
		if len(provider.Models) == 0 && providerName == c.Provider && c.Model != "" {
			out = append(out, c.Selection)
		}
	}
	sort.Strings(out)
	return out
}

func ParseSelection(selection string) (provider, model string, err error) {
	selection = strings.TrimSpace(selection)
	before, after, ok := strings.Cut(selection, ":")
	if !ok || before == "" || after == "" {
		return "", "", fmt.Errorf("model selection must use provider:model")
	}
	return before, after, nil
}

func loadFileConfig(path string) (fileConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileConfig{}, false, nil
		}
		return fileConfig{}, false, err
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fileConfig{}, false, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	return cfg, true, nil
}

func legacyConfig() fileConfig {
	return fileConfig{
		Default: DefaultSelection,
		Providers: map[string]ProviderConfig{
			DefaultProvider: {
				Type:    ProviderTypeOpenAICompatible,
				BaseURL: first(os.Getenv("OPENAI_BASE_URL"), DefaultBaseURL),
				APIKey:  os.Getenv("OPENAI_API_KEY"),
			},
		},
	}
}

func resolveContextConfig(file contextFileConfig) (ContextConfig, error) {
	cfg := ContextConfig{
		MaxPromptTokens:  DefaultMaxPromptTokens,
		TailTurns:        DefaultTailTurns,
		SummaryMaxTokens: DefaultSummaryMaxTokens,
		AutoCompact:      true,
	}
	if file.MaxPromptTokens != nil {
		cfg.MaxPromptTokens = *file.MaxPromptTokens
	}
	if file.TailTurns != nil {
		cfg.TailTurns = *file.TailTurns
	}
	if file.SummaryMaxTokens != nil {
		cfg.SummaryMaxTokens = *file.SummaryMaxTokens
	}
	if file.AutoCompact != nil {
		cfg.AutoCompact = *file.AutoCompact
	}
	if cfg.MaxPromptTokens <= 0 {
		return ContextConfig{}, fmt.Errorf("context.maxPromptTokens must be greater than 0")
	}
	if cfg.TailTurns < 1 {
		return ContextConfig{}, fmt.Errorf("context.tailTurns must be at least 1")
	}
	if cfg.SummaryMaxTokens <= 0 {
		return ContextConfig{}, fmt.Errorf("context.summaryMaxTokens must be greater than 0")
	}
	return cfg, nil
}

func validateProviders(providers map[string]ProviderConfig) error {
	if len(providers) == 0 {
		return fmt.Errorf("config must define at least one provider")
	}
	for name := range providers {
		if name == "" {
			return fmt.Errorf("provider name cannot be empty")
		}
		if strings.Contains(name, ":") {
			return fmt.Errorf("provider name %q cannot contain ':'", name)
		}
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func resolveHomeDir(homeDir string) string {
	if homeDir != "" {
		return homeDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
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
