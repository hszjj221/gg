package app

import (
	"fmt"
	"log/slog"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/userprofile"
)

type Options struct {
	Config          config.Config
	ProviderFactory ProviderFactory
	Store           *session.Store
	History         []agent.Message
	Summary         *session.SummaryEntry
	Skills          skills.Set
	ModelRecorded   bool
	// ToolProviders overrides the built-in registry for an embedding host.
	// Nil uses the default providers; an empty slice disables tools.
	ToolProviders []ToolProvider
	// Profile and MemoryStore carry the personal layer. MemoryStore may be
	// nil; NewService then falls back to a store rooted at Config.Memory.Dir.
	// It is a memory.StoreAPI so a per-workspace overlay and the plain
	// global store are interchangeable here.
	Profile     userprofile.Profile
	MemoryStore memory.StoreAPI
	// Log receives operational warnings, e.g. when a tool provider fails
	// to build and degrades to absent. Nil means slog.Default(); the daemon
	// passes its stderr logger so failures are visible in daemon logs.
	Log *slog.Logger
	// Degraded is the registry this service reports provider build
	// outcomes to. Nil means a private registry; the daemon workspace
	// shares one across its sessions so recovery in any session clears
	// the provider everywhere.
	Degraded *DegradedRegistry
}

// NewService creates a conversation facade. Its owner must call Close after
// the last turn. Persistent callers use NewSessionService to restore state.
func NewService(options Options) *Service {
	tools := newConversationTools(options)
	state := &conversation{
		cfg: options.Config, store: options.Store,
		history: append([]agent.Message(nil), options.History...),
		summary: cloneSummaryEntry(options.Summary), modelRecorded: options.ModelRecorded,
	}
	return &Service{turnExecutor: &turnExecutor{
		conversation:    state,
		promptBuilder:   &promptBuilder{profile: options.Profile, memStore: tools.memStore, skillSet: options.Skills},
		providerFactory: options.ProviderFactory,
		queue:           &agent.MessageQueue{}, tools: tools,
	}}
}

// NewSessionService restores persisted state once for CLI, daemon and forks.
// A non-empty selectionOverride keeps the caller's explicit model selection.
func NewSessionService(options Options, store *session.Store, loaded session.Loaded, selectionOverride string) (*Service, error) {
	selection := selectionOverride
	if selection == "" && loaded.LastModel != nil {
		selection = loaded.LastModel.Selection
	}
	if (selectionOverride == "" && loaded.LastModel != nil) || (selection != "" && selection != options.Config.Selection) {
		cfg, err := options.Config.WithSelection(selection)
		if err != nil {
			return nil, err
		}
		options.Config = cfg
	}
	if loaded.LastSummary != nil {
		through := loaded.LastSummary.ThroughMessageCount
		if through < 0 || through > len(loaded.Messages) {
			return nil, fmt.Errorf("summary position %d is outside loaded history of %d messages", through, len(loaded.Messages))
		}
	}
	options.Store = store
	options.History = loaded.Messages
	options.Summary = loaded.LastSummary
	options.ModelRecorded = loaded.LastModel != nil && loaded.LastModel.Selection == options.Config.Selection
	return NewService(options), nil
}
