package app

import (
	"fmt"
	"strings"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/userprofile"
)

// Personal bundles the personalization state of one gg installation: the
// user profile and the structured memory store.
type Personal struct {
	Profile userprofile.Profile
	Store   *memory.Store
}

// SetupPersonal prepares the personal layer: it ensures the memory directory
// layout, migrates a legacy ~/.gg/memory.md once, prunes expired daily logs,
// and loads the user profile. The returned notice is non-empty when the
// migration moved data, pruning deleted logs, or something non-fatal went
// wrong; callers may print it.
//
// SetupPersonal never fails on profile or prune problems: a broken
// ~/.gg/USER.md or an undeletable daily log must not brick gg. Only
// EnsureLayout and migration failures are fatal, because without a usable
// memory directory the memory tools would misbehave.
func SetupPersonal(cfg config.Config) (Personal, string, error) {
	personal := Personal{Store: memory.NewStore(cfg.Memory.Dir)}
	var notices []string
	if cfg.Memory.Enabled {
		if err := personal.Store.EnsureLayout(); err != nil {
			return Personal{}, "", err
		}
		migrated, err := personal.Store.MigrateFromFile(cfg.MemoryPath)
		if err != nil {
			return Personal{}, "", err
		}
		if migrated != "" {
			notices = append(notices, migrated)
		}
		if pruned, err := personal.Store.PruneDaily(cfg.Memory.DailyLogRetentionDays); err != nil {
			notices = append(notices, fmt.Sprintf("warning: could not prune daily memory logs: %v", err))
		} else if pruned > 0 {
			notices = append(notices, fmt.Sprintf("pruned %d expired daily memory logs", pruned))
		}
	}
	if profile, err := userprofile.Load(cfg.UserFile); err != nil {
		notices = append(notices, fmt.Sprintf("warning: could not load user profile: %v", err))
	} else {
		personal.Profile = profile
	}
	return personal, strings.Join(notices, "; "), nil
}
