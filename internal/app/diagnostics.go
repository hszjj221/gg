package app

import (
	"sort"
	"strings"
	"sync"
)

// DegradedProvider names a tool capability whose provider failed its most
// recent Build, with the reason it degraded to absent. It is the
// machine-readable form of the "why did my tools disappear" signal:
// failures are logged when they happen, and the latest set is exposed via
// Service.DegradedProviders and the system.info diagnostic endpoint.
type DegradedProvider struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// DegradedRegistry tracks the latest per-provider build outcome. Services
// that share a registry (one daemon workspace) report every build, so a
// provider that recovers in any session is cleared everywhere instead of
// lingering as a stale failure from a session that hasn't run since.
// A nil-error report clears the provider. Safe for concurrent use.
type DegradedRegistry struct {
	mu     sync.Mutex
	failed map[string]string
}

// Report records one provider's build outcome: a non-nil err marks it
// degraded with the given reason, a nil err clears a previous failure.
func (r *DegradedRegistry) Report(name, reason string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		delete(r.failed, name)
		return
	}
	if r.failed == nil {
		r.failed = make(map[string]string)
	}
	r.failed[name] = reason
}

// List returns the currently degraded providers, sorted by name for
// stable output.
func (r *DegradedRegistry) List() []DegradedProvider {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]DegradedProvider, 0, len(r.failed))
	for name, reason := range r.failed {
		out = append(out, DegradedProvider{Name: name, Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// sanitizeProviderReason redacts the user's home directory from a provider
// build error before it is exposed to clients: absolute paths under ~ are
// a local detail the public DTO boundary keeps private
// (docs/architecture.md). The full error is still written to the daemon
// log; the diagnostic reason keeps enough context (which file, which
// server) to act on.
func sanitizeProviderReason(homeDir, reason string) string {
	if homeDir == "" {
		return reason
	}
	return strings.ReplaceAll(reason, homeDir, "~")
}
