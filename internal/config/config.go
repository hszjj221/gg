package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/userprofile"
)

const (
	DefaultProvider  = "openai"
	DefaultBaseURL   = "https://api.openai.com/v1"
	DefaultModel     = "gpt-4.1"
	DefaultSelection = DefaultProvider + ":" + DefaultModel

	DefaultMaxPromptTokens  = 24000
	DefaultMaxOutputTokens  = 4096
	DefaultTailTurns        = 6
	DefaultSummaryMaxTokens = 1200
	DefaultMemoryMaxTokens  = 1200

	DefaultDailyLogTailTokens    = 500
	DefaultDailyLogRetentionDays = 90

	ProviderTypeOpenAICompatible = "openai-compatible"
)

type Options struct {
	APIKey         string
	BaseURL        string
	Model          string
	SessionDir     string
	CWD            string
	HomeDir        string
	NoMemory       bool
	NoContextFiles bool
}

type ProviderConfig struct {
	Type    string   `json:"type"`
	BaseURL string   `json:"baseURL"`
	APIKey  string   `json:"apiKey"`
	Models  []string `json:"models"`
}

type ContextConfig struct {
	MaxPromptTokens  int  `json:"maxPromptTokens"`
	MaxOutputTokens  int  `json:"maxOutputTokens"`
	TailTurns        int  `json:"tailTurns"`
	SummaryMaxTokens int  `json:"summaryMaxTokens"`
	AutoCompact      bool `json:"autoCompact"`
}

type MemoryConfig struct {
	Enabled               bool   `json:"enabled"`
	MaxPromptTokens       int    `json:"maxPromptTokens"`
	Dir                   string `json:"dir"`
	DailyLogTailTokens    int    `json:"dailyLogTailTokens"`
	DailyLogRetentionDays int    `json:"dailyLogRetentionDays"`
}

// ArtifactConfig locates the artifact store's persisted state.
type ArtifactConfig struct {
	Dir string
}

// LibraryConfig locates the user's file library.
type LibraryConfig struct {
	Dir string
}

// SchedulerConfig locates the scheduler's persisted state.
type SchedulerConfig struct {
	Dir string `json:"dir"`
}

type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	// EmbedBaseURL/EmbedAPIKey optionally override the chat provider's
	// endpoint for embeddings (used by `gg kb` and the kb_search tool).
	// Resolved from GG_EMBED_BASE_URL / GG_EMBED_API_KEY.
	EmbedBaseURL string
	EmbedAPIKey  string
	Provider     string
	ProviderType string
	Selection    string
	Providers    map[string]ProviderConfig
	Context      ContextConfig
	Memory       MemoryConfig
	Scheduler    SchedulerConfig
	Artifacts    ArtifactConfig
	Library      LibraryConfig
	// MemoryPath is the legacy single-file memory location (~/.gg/memory.md),
	// kept only for the one-time migration into Memory.Dir. New code uses
	// Memory.Dir.
	MemoryPath string
	// UserFile is the user profile path (~/.gg/USER.md).
	UserFile string
	// HomeDir is the resolved home directory (options.HomeDir or os.UserHomeDir).
	HomeDir        string
	SessionDir     string
	KBDir          string
	CWD            string
	NoContextFiles bool

	apiKeyOverride  string
	baseURLOverride string
}

type fileConfig struct {
	Default   string                    `json:"default"`
	Providers map[string]ProviderConfig `json:"providers"`
	Context   contextFileConfig         `json:"context"`
	Memory    memoryFileConfig          `json:"memory"`
	Scheduler schedulerFileConfig       `json:"scheduler"`
	Artifacts dirFileConfig             `json:"artifacts"`
	Library   dirFileConfig             `json:"library"`
}

type contextFileConfig struct {
	MaxPromptTokens  *int  `json:"maxPromptTokens"`
	MaxOutputTokens  *int  `json:"maxOutputTokens"`
	TailTurns        *int  `json:"tailTurns"`
	SummaryMaxTokens *int  `json:"summaryMaxTokens"`
	AutoCompact      *bool `json:"autoCompact"`
}

type memoryFileConfig struct {
	Enabled               *bool   `json:"enabled"`
	MaxPromptTokens       *int    `json:"maxPromptTokens"`
	Dir                   *string `json:"dir"`
	DailyLogTailTokens    *int    `json:"dailyLogTailTokens"`
	DailyLogRetentionDays *int    `json:"dailyLogRetentionDays"`
}

type schedulerFileConfig struct {
	Dir *string `json:"dir"`
}

// dirFileConfig is a generic {"dir": ...} file override for a state dir.
type dirFileConfig struct {
	Dir *string `json:"dir"`
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
	memoryConfig, err := resolveMemoryConfig(home, cfgFile.Memory, options.NoMemory)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Providers:       cfgFile.Providers,
		Context:         contextConfig,
		Memory:          memoryConfig,
		MemoryPath:      filepath.Join(home, ".gg", "memory.md"),
		UserFile:        userprofile.DefaultPath(home),
		HomeDir:         home,
		SessionDir:      sessionDir,
		KBDir:           filepath.Join(home, ".gg", "kb"),
		Scheduler:       resolveSchedulerConfig(home, cfgFile.Scheduler),
		Artifacts:       ArtifactConfig{Dir: resolveStateDir(home, cfgFile.Artifacts.Dir, "artifacts")},
		Library:         LibraryConfig{Dir: resolveStateDir(home, cfgFile.Library.Dir, "library")},
		CWD:             cwd,
		NoContextFiles:  options.NoContextFiles,
		EmbedBaseURL:    os.Getenv("GG_EMBED_BASE_URL"),
		EmbedAPIKey:     os.Getenv("GG_EMBED_API_KEY"),
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
		MaxOutputTokens:  DefaultMaxOutputTokens,
		TailTurns:        DefaultTailTurns,
		SummaryMaxTokens: DefaultSummaryMaxTokens,
		AutoCompact:      true,
	}
	if file.MaxPromptTokens != nil {
		cfg.MaxPromptTokens = *file.MaxPromptTokens
	}
	if file.MaxOutputTokens != nil {
		cfg.MaxOutputTokens = *file.MaxOutputTokens
	}
	if cfg.MaxOutputTokens <= 0 {
		return ContextConfig{}, fmt.Errorf("context.maxOutputTokens must be greater than 0")
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

// resolveStateDir locates a ~/.gg/<name> state dir, honoring an optional
// configured dir (~/ prefix supported).
func resolveStateDir(home string, configured *string, name string) string {
	dir := filepath.Join(home, ".gg", name)
	if configured != nil && strings.TrimSpace(*configured) != "" {
		dir = expandHome(home, *configured)
	}
	return dir
}

// resolveSchedulerConfig locates the scheduler state dir, defaulting to
// ~/.gg/scheduler. A configured dir supports the ~/ prefix like memory.dir.
func resolveSchedulerConfig(home string, file schedulerFileConfig) SchedulerConfig {
	return SchedulerConfig{Dir: resolveStateDir(home, file.Dir, "scheduler")}
}

func resolveMemoryConfig(home string, file memoryFileConfig, disabledByCLI bool) (MemoryConfig, error) {
	cfg := MemoryConfig{
		Enabled:               true,
		MaxPromptTokens:       DefaultMemoryMaxTokens,
		Dir:                   memory.DefaultDir(home),
		DailyLogTailTokens:    DefaultDailyLogTailTokens,
		DailyLogRetentionDays: DefaultDailyLogRetentionDays,
	}
	if file.Enabled != nil {
		cfg.Enabled = *file.Enabled
	}
	if file.MaxPromptTokens != nil {
		cfg.MaxPromptTokens = *file.MaxPromptTokens
	}
	if file.Dir != nil {
		cfg.Dir = expandHome(home, *file.Dir)
	}
	if file.DailyLogTailTokens != nil {
		cfg.DailyLogTailTokens = *file.DailyLogTailTokens
	}
	if file.DailyLogRetentionDays != nil {
		cfg.DailyLogRetentionDays = *file.DailyLogRetentionDays
	}
	if disabledByCLI {
		cfg.Enabled = false
	}
	if cfg.MaxPromptTokens <= 0 {
		return MemoryConfig{}, fmt.Errorf("memory.maxPromptTokens must be greater than 0")
	}
	if cfg.DailyLogTailTokens < 0 {
		return MemoryConfig{}, fmt.Errorf("memory.dailyLogTailTokens must not be negative")
	}
	if cfg.DailyLogRetentionDays < 0 {
		return MemoryConfig{}, fmt.Errorf("memory.dailyLogRetentionDays must not be negative")
	}
	if strings.TrimSpace(cfg.Dir) == "" {
		return MemoryConfig{}, fmt.Errorf("memory.dir must not be empty")
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

// expandHome expands a leading "~/" in a configured path against home.
// Paths without the prefix are returned unchanged.
func expandHome(home, path string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
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
