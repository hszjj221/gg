package app

import (
	"context"

	"github.com/hszjj221/gg/internal/agent"
)

// DegradedProviders returns the providers that failed their most recent
// toolset build, with reasons. Empty means the last turn built the full
// toolset. Safe for concurrent use.
func (s *Service) DegradedProviders() []DegradedProvider {
	return s.tools.degraded.List()
}

func (s *Service) Queue() *agent.MessageQueue {
	return s.queue
}

func (s *Service) Run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	result, err := s.run(ctx, prompt, onEvent, approver)
	result.TreeItems = s.treeItems()
	return result, err
}

// Service exposes conversation use cases to adapters. State, turn execution,
// prompt construction and tool resources each have one owner behind this facade.
type Service struct{ *turnExecutor }

func (s *Service) run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	if err := s.beginTurn(); err != nil {
		return Result{}, err
	}
	defer s.endTurn()
	if result, ok, err := s.handleControlCommand(ctx, prompt); ok || err != nil {
		return result, err
	}
	return s.execute(ctx, prompt, onEvent, approver)
}

func (s *Service) Close() error { return s.tools.Close() }

func (s *Service) buildTools(ctx context.Context, provider agent.Provider) []agent.Tool {
	return s.tools.Build(ctx, s.cfg, provider)
}

func (s *Service) systemMessages() ([]agent.Message, error) {
	return s.promptBuilder.systemMessages(s.cfg)
}
