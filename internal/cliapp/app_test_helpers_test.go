package cliapp

import (
	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
)

type turnExecutor = app.Service

func newTurnExecutor(cfg config.Config, providerFactory func(config.Config) agent.Provider, store *session.Store, history []agent.Message, summary *session.SummaryEntry, skillSet skills.Set, modelRecorded bool) *app.Service {
	return app.NewService(app.Options{
		Config:          cfg,
		ProviderFactory: providerFactory,
		Store:           store,
		History:         history,
		Summary:         summary,
		Skills:          skillSet,
		ModelRecorded:   modelRecorded,
	})
}
