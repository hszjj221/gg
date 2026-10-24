package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/standingauth"
)

// turnExecutor coordinates a provider turn using state, prompt and tool
// components. It has no transport or application-command responsibilities.
type turnExecutor struct {
	*conversation
	*promptBuilder
	providerFactory ProviderFactory
	queue           *agent.MessageQueue
	tools           *conversationTools
}

func (s *turnExecutor) execute(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	if s.providerFactory == nil {
		return Result{}, fmt.Errorf("provider factory is not configured")
	}
	preparedPrompt, err := preparePrompt(prompt, s.skillSet)
	if err != nil {
		return Result{}, err
	}
	if err := s.ensureModelRecorded(); err != nil {
		return Result{}, err
	}
	systemMessages, err := s.promptBuilder.systemMessages(s.cfg)
	if err != nil {
		return Result{}, err
	}
	if err := s.recoverPendingTools(); err != nil {
		return Result{}, err
	}
	user := agent.Message{Role: agent.RoleUser, Content: preparedPrompt, Timestamp: time.Now().UnixMilli()}
	if err := s.persistMessage(user); err != nil {
		return Result{}, err
	}
	provider := &observedProvider{Provider: s.providerFactory(s.cfg), model: s.cfg.Selection}
	summaryUsage := agent.Usage{}
	// Production config resolves HomeDir. Embedders without one retain P1
	// policy rather than trusting a standing-auth directory under the CWD.
	if s.cfg.HomeDir != "" {
		approver = standingauth.Approver{Inner: approver, Store: standingauth.New(standingauth.DefaultDir(s.cfg.HomeDir))}
	}
	runner := agent.NewRunnerWithOptions(provider, s.tools.Build(ctx, s.cfg, provider), agent.RunnerOptions{
		Approver:      approver,
		OnMessage:     s.persistMessage,
		DrainMessages: s.queue.DrainSteering,
		BeforeRequest: func(ctx context.Context, req agent.Request) (agent.Request, error) {
			prepared, usage, err := s.prepareRequest(ctx, provider, systemMessages, req)
			summaryUsage = summaryUsage.Add(usage)
			return prepared, err
		},
	})
	reply, runErr := runner.Run(ctx, nil, onEvent)
	usage := summaryUsage.Add(runner.Usage())
	persistErr := s.appendUsage(runner.Usage())
	return Result{Content: reply.Content, Usage: usage, ModelName: s.cfg.Selection}, errors.Join(runErr, persistErr)
}
