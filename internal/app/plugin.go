package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/tools"
)

// ToolProvider is the Tool SPI: one capability's builder and platform gate.
// Register it with NewRegistry or inject it through Options.ToolProviders.
type ToolProvider struct {
	// Name is the capability identifier advertised to clients. Empty means
	// the provider is always on and not advertised (core tools).
	Name string
	// Build returns the capability's tools. A nil slice (or an error) means
	// the capability degrades to absent for this turn — Chromium not
	// installed, Google not connected, MCP server unreachable, ...
	Build func(ctx context.Context, tc ToolContext) ([]agent.Tool, error)
	// Available reports whether the capability can be provided on this
	// platform at all. Nil means always available. A provider that is not
	// available is neither built nor advertised (e.g. computer tools off
	// Linux/macOS).
	Available func() bool
}

// ToolContext carries everything a provider needs to build its tools.
type ToolContext struct {
	Config      config.Config
	Provider    agent.Provider // nil when only definitions are needed (/context)
	ReadRoots   []string
	MemStore    memory.ToolStore
	Location    *time.Location
	LocationErr error
	BrowserPool *tools.BrowserSessionPool
	// Log receives provider-local operational warnings (e.g. one MCP
	// server failing while others work). Never nil in buildTools; tests
	// that build a ToolContext by hand should treat nil as slog.Default().
	Log *slog.Logger
	// Memoized holds per-Service resources so providers don't redo static
	// probes or discard reusable clients every turn.
	Memoized *ToolMemo
}

// Registry is an ordered snapshot of Tool SPI registrations. Construct it at
// the host's composition boundary; it has no process-global registration hooks.
// The zero value is an empty registry. Build and Capabilities share platform gates;
// ForProfile selects the same providers for construction and capability reporting.
type Registry struct {
	providers []ToolProvider
}

// NewRegistry registers providers in order, copying the slice so later host
// mutations cannot change a conversation's assembly. Names and Build functions
// are supplied by trusted in-tree code or an embedding host, as with ToolProviders.
func NewRegistry(providers ...ToolProvider) Registry {
	return Registry{providers: append([]ToolProvider{}, providers...)}
}

// Providers returns a copy of the registrations, including unavailable ones.
// Hosts can extend a registry or carry its selection into a forked conversation.
func (r Registry) Providers() []ToolProvider {
	return append([]ToolProvider{}, r.providers...)
}

// ForProfile resolves Core's enable list in registration order. Coding (including
// an empty profile) retains every registration, including host extensions. Personal
// keeps unnamed core providers and an explicit subset of named capabilities.
// Config resolution validates profiles before assembly; this adds no config syntax.
func (r Registry) ForProfile(profile string) Registry {
	if profile != config.ProfilePersonal {
		return r
	}
	var enabled []ToolProvider
	for _, p := range r.providers {
		switch p.Name {
		case "", "memory", "kb", "artifact", "connector", "browser":
			enabled = append(enabled, p)
		}
	}
	return NewRegistry(enabled...)
}

// Capabilities returns named, platform-available providers in registration order.
// A missing runtime prerequisite can still make Build return no tools.
func (r Registry) Capabilities() []string {
	names := make([]string, 0, len(r.providers))
	for _, p := range r.providers {
		if p.Name != "" && (p.Available == nil || p.Available()) {
			names = append(names, p.Name)
		}
	}
	return names
}

// Build assembles the profile's tools in registration order. Disabled and
// unavailable providers are skipped; a failed provider contributes no tools.
// report, when non-nil, receives every attempted provider's outcome (including
// success), leaving logging and degradation policy with the host. No tool is
// executed and no authorization is granted here.
func (r Registry) Build(ctx context.Context, tc ToolContext, report func(name string, err error)) []agent.Tool {
	var out []agent.Tool
	for _, p := range r.ForProfile(tc.Config.Profile).providers {
		if p.Available != nil && !p.Available() {
			continue
		}
		built, err := p.Build(ctx, tc)
		if report != nil {
			name := p.Name
			if name == "" {
				name = "core"
			}
			report(name, err)
		}
		if err == nil {
			out = append(out, built...)
		}
	}
	return out
}

// ConnectorProvider is the token-backed Connector SPI. Name is the existing
// token-store identifier, not an advertised capability. Build receives the
// current stored token and host context; it selects tools for the granted scopes.
// Clients must keep using Core's dispatch/approval path and existing token store.
// C1 adds no independent startup/Close hooks; existing per-conversation resources
// remain host-owned through ToolContext. The Google pilot adds no background work.
type ConnectorProvider struct {
	Name      string
	Available func() bool // nil means always available
	Build     func(context.Context, ToolContext, *connector.Store, connector.Token) ([]agent.Tool, error)
}

// ConnectorTools registers connectors under the existing "connector" capability.
// It preserves expected-not-connected versus operational failure semantics and
// re-reads tokens each turn. OAuth and refresh remain the implementation's concern.
func ConnectorTools(providers ...ConnectorProvider) ToolProvider {
	providers = append([]ConnectorProvider(nil), providers...)
	return ToolProvider{Name: "connector", Build: func(ctx context.Context, tc ToolContext) ([]agent.Tool, error) {
		var store *connector.Store
		var out []agent.Tool
		for _, p := range providers {
			if p.Available != nil && !p.Available() {
				continue
			}
			// Do not open the token store when every connector is unavailable.
			if store == nil {
				var err error
				store, err = connector.Open(tc.Config.Connectors.Dir)
				if err != nil {
					return nil, err
				}
			}
			tok, err := store.Load(p.Name)
			if errors.Is(err, connector.ErrNotConnected) {
				continue
			}
			if err != nil {
				return nil, err
			}
			built, err := p.Build(ctx, tc, store, tok)
			if err != nil {
				return nil, err
			}
			out = append(out, built...)
		}
		return out, nil
	}}
}
