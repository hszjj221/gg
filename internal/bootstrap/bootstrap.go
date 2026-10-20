// Package bootstrap builds the dependencies shared by the CLI and daemon
// composition roots. It exists so provider, repository, and skill
// construction cannot drift between entry points.
package bootstrap

import (
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/provider"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
)

// ProviderFactory returns the caller's override when supplied, otherwise the
// single provider factory. The CLI, job runner, and daemon all use this.
func ProviderFactory(override app.ProviderFactory) app.ProviderFactory {
	if override != nil {
		return override
	}
	return provider.New
}

// Repository returns the file-backed session repository for cfg.
func Repository(cfg config.Config) *session.FileRepository {
	return session.NewFileRepository(cfg.SessionDir)
}

// LoadSkills loads the skill set unless skills are disabled.
func LoadSkills(cwd, home string, disabled bool) (skills.Set, error) {
	if disabled {
		return skills.Set{}, nil
	}
	return skills.Load(skills.LoadOptions{CWD: cwd, HomeDir: home})
}
