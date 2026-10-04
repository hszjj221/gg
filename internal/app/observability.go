package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/runlog"
)

type runIDKey struct{}
type requestKindKey struct{}

// observedProvider also wraps compaction and subagent requests; request IDs
// stay unique when read-only subagents share the provider concurrently.
type observedProvider struct {
	agent.Provider
	model    string
	sequence atomic.Int64
}

func (p *observedProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	kind, _ := ctx.Value(requestKindKey{}).(string)
	if kind == "" {
		kind = "turn"
	}
	logger := runlog.Logger(ctx, slog.Default()).With("requestID", p.sequence.Add(1), "requestKind", kind, "model", p.model)
	ctx = runlog.WithLogger(ctx, logger)
	started := time.Now()
	logger.DebugContext(ctx, "model request started", "messageCount", len(req.Messages), "toolCount", len(req.Tools), "maxOutputTokens", req.MaxOutputTokens)
	reply, err := p.Provider.Complete(ctx, req, onEvent)
	logger.DebugContext(ctx, "model request finished", "durationMs", float64(time.Since(started).Microseconds())/1000, "outcome", runlog.Outcome(err), "errorType", fmt.Sprintf("%T", err), "promptTokens", reply.Usage.PromptTokens, "completionTokens", reply.Usage.CompletionTokens, "totalTokens", reply.Usage.TotalTokens)
	return reply, err
}
