package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/runlog"
)

const defaultMaxTurns = 32

// maxParallelToolCalls bounds how many tool executions run concurrently
// within one assistant turn. Tool resolution and approval still happen
// sequentially, in call order, before any execution starts, so approval
// prompts never interleave.
const maxParallelToolCalls = 4

type RunnerOptions struct {
	MaxTurns int
	Approver Approver
	// BeforeRequest may rebuild the model context without rewriting the transcript.
	BeforeRequest func(context.Context, Request) (Request, error)
	// OnMessage runs before executing any tools requested by the message.
	// Returning an error stops execution, for example when persistence fails.
	OnMessage     func(Message) error
	DrainMessages func() []Message
}

type Runner struct {
	provider      Provider
	tools         map[string]Tool
	defs          []ToolDefinition
	maxTurns      int
	approver      Approver
	beforeRequest func(context.Context, Request) (Request, error)
	onMessage     func(Message) error
	drainMessages func() []Message

	transcript []Message
	usage      Usage
}

func NewRunner(provider Provider, tools []Tool) *Runner {
	return NewRunnerWithOptions(provider, tools, RunnerOptions{})
}

func NewRunnerWithOptions(provider Provider, tools []Tool, options RunnerOptions) *Runner {
	toolMap := make(map[string]Tool, len(tools))
	defs := make([]ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		toolMap[tool.Name()] = tool
		defs = append(defs, tool.Definition())
	}
	maxTurns := options.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	return &Runner{provider: provider, tools: toolMap, defs: defs, maxTurns: maxTurns, approver: options.Approver, beforeRequest: options.BeforeRequest, onMessage: options.OnMessage, drainMessages: options.DrainMessages}
}

func (r *Runner) Transcript() []Message {
	out := make([]Message, len(r.transcript))
	copy(out, r.transcript)
	return out
}

func (r *Runner) Usage() Usage {
	return r.usage
}

func (r *Runner) Run(ctx context.Context, messages []Message, onEvent func(Event)) (AssistantMessage, error) {
	current := append([]Message(nil), messages...)
	r.transcript = append([]Message(nil), messages...)
	r.usage = Usage{}
	addQueued := func() (int, error) {
		if r.drainMessages == nil {
			return 0, nil
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		queued := r.drainMessages()
		for _, message := range queued {
			if err := r.record(message); err != nil {
				return 0, err
			}
			current = append(current, message)
			emitToolEvent(onEvent, Event{Type: EventUserMessage, Text: message.Content})
		}
		return len(queued), nil
	}

	for turn := 0; turn < r.maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return AssistantMessage{}, err
		}
		if _, err := addQueued(); err != nil {
			return AssistantMessage{}, err
		}
		req := Request{Messages: current, Tools: r.defs}
		if r.beforeRequest != nil {
			var err error
			req, err = r.beforeRequest(ctx, req)
			if err != nil {
				return AssistantMessage{}, err
			}
		}
		if err := ctx.Err(); err != nil {
			return AssistantMessage{}, err
		}
		reply, err := r.provider.Complete(ctx, req, onEvent)
		r.usage = r.usage.Add(reply.Usage)
		if err != nil {
			// Partial tool arguments must never be persisted as executable calls.
			msg := reply.Message
			msg.Role, msg.ToolCalls, msg.Error = RoleAssistant, nil, err.Error()
			msg.StopReason = reply.StopReason
			if msg.StopReason == "" {
				msg.StopReason = StopReasonError
			}
			if errors.Is(err, context.Canceled) {
				msg.StopReason = StopReasonCanceled
			}
			if msg.Content == "" {
				msg.Content = "Request interrupted: " + err.Error()
			}
			return reply, errors.Join(err, r.record(msg))
		}
		reply.Message.StopReason = reply.StopReason
		if err := r.record(reply.Message); err != nil {
			return AssistantMessage{}, err
		}
		current = append(current, reply.Message)

		if len(reply.ToolCalls) == 0 && reply.StopReason != StopReasonToolUse {
			count, err := addQueued()
			if err != nil {
				return reply, err
			}
			if count > 0 {
				continue
			}
			return reply, nil
		}

		results := r.executeToolCalls(ctx, reply.ToolCalls, onEvent)
		for i, call := range reply.ToolCalls {
			result := results[i]
			r.usage = r.usage.Add(result.Usage)
			content := resultText(result)
			toolMessage := Message{
				Role:       RoleTool,
				Content:    content,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				ContentBlocks: []ContentBlock{{
					Type: ContentText,
					Text: content,
				}},
			}
			current = append(current, toolMessage)
			if result.IsError {
				toolMessage.Error = content
			}
			if err := r.record(toolMessage); err != nil {
				return AssistantMessage{}, err
			}
		}
		if err := ctx.Err(); err != nil {
			return AssistantMessage{}, err
		}
	}

	return AssistantMessage{}, fmt.Errorf("agent exceeded %d turns", r.maxTurns)
}

func (r *Runner) record(message Message) error {
	if r.onMessage != nil {
		if err := r.onMessage(message); err != nil {
			return err
		}
	}
	r.transcript = append(r.transcript, message)
	return nil
}

// pendingCall is a tool call that cleared the sequential resolve+approval
// phase and is ready to execute, or already has its final result.
type pendingCall struct {
	call    ToolCall
	tool    Tool
	summary string
	decided *ToolResult // non-nil: result determined, skip execution
}

// executeToolCalls runs one assistant message's tool calls: resolve and
// approve sequentially in call order. Parallel-safe calls overlap within a
// bounded group; other calls form execution barriers. Results remain in the
// original call order for a stable transcript.
func (r *Runner) executeToolCalls(ctx context.Context, calls []ToolCall, onEvent func(Event)) []ToolResult {
	pending := make([]pendingCall, 0, len(calls))
	for _, call := range calls {
		tool, summary, decided := r.prepareToolCall(ctx, call, onEvent)
		pending = append(pending, pendingCall{call: call, tool: tool, summary: summary, decided: decided})
	}
	results := make([]ToolResult, len(pending))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallelToolCalls)
	for i, p := range pending {
		if p.decided != nil {
			results[i] = *p.decided
			continue
		}
		parallel, ok := p.tool.(ParallelTool)
		if !ok || !parallel.ParallelSafe() {
			wg.Wait()
			results[i] = r.startPreparedCall(ctx, p.tool, p.call, p.summary, onEvent)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = r.startPreparedCall(ctx, p.tool, p.call, p.summary, onEvent)
		}()
	}
	wg.Wait()
	return results
}

// prepareToolCall resolves the tool and obtains approval, emitting the
// start event. It returns the tool to execute, or a non-nil result when
// the call is already decided (unknown tool, describe error, denied,
// cancelled) — in that case the finish event is emitted here and the
// call skips the execution phase.
func (r *Runner) prepareToolCall(ctx context.Context, call ToolCall, onEvent func(Event)) (prepared Tool, text string, decided *ToolResult) {
	started := time.Now()
	reason := ""
	defer func() {
		outcome := "ready"
		if decided != nil {
			outcome = "skipped"
		}
		runlog.Logger(ctx, slog.Default()).DebugContext(ctx, "tool preparation finished", "tool", call.Name, "toolCallID", call.ID, "durationMs", float64(time.Since(started).Microseconds())/1000, "outcome", outcome, "reason", reason)
	}()
	if err := ctx.Err(); err != nil {
		reason = runlog.Outcome(err)
		result := toolError(fmt.Errorf("tool not executed: %w", err))
		emitToolFinish(onEvent, call, call.Name, result)
		return nil, "", &result
	}
	tool, ok := r.tools[call.Name]
	if !ok {
		reason = "unknown_tool"
		summary, details := fallbackToolSummary(call)
		emitToolEvent(onEvent, Event{Type: EventToolCallStart, ToolCallID: call.ID, ToolName: call.Name, Summary: summary, Details: details})
		result := toolError(fmt.Errorf("unknown tool %q", call.Name))
		emitToolFinish(onEvent, call, summary, result)
		return nil, "", &result
	}
	req, reqErr := describeToolCall(tool, call)
	summary := req.Summary
	details := req.Details
	// A tool may pre-approve its own request for a narrow, path-scoped
	// exemption (write/edit inside the workspace agent area). The
	// annotation keeps the exemption visible in the event stream.
	preApproved := reqErr == nil && req.PreApproved
	if preApproved {
		summary += " [agent area]"
	}
	emitToolEvent(onEvent, Event{Type: EventToolCallStart, ToolCallID: call.ID, ToolName: call.Name, Summary: summary, Details: details})
	if reqErr != nil {
		reason = "invalid_approval"
		result := toolError(fmt.Errorf("approval request for tool %q failed: %w", call.Name, reqErr))
		emitToolFinish(onEvent, call, summary, result)
		return nil, "", &result
	}
	if r.approver != nil {
		if _, ok := tool.(ApprovalDescriber); ok {
			if req.ToolName == "" {
				req.ToolName = call.Name
			}
			if len(req.Arguments) == 0 {
				req.Arguments = call.Arguments
			}
			// A pre-approved request skips the approver entirely: the tool
			// asserted a narrow, registry-allowed exemption (agent-area
			// writes). The start/finish events above and below are still
			// emitted, with the summary annotated for transparency.
			if !req.PreApproved {
				started := time.Now()
				decision, err := r.approver.Approve(ctx, req)
				outcome := runlog.Outcome(err)
				if err == nil && !decision.Allow {
					outcome = "denied"
				}
				runlog.Logger(ctx, slog.Default()).DebugContext(ctx, "tool approval finished", "tool", call.Name, "toolCallID", call.ID, "durationMs", float64(time.Since(started).Microseconds())/1000, "outcome", outcome)
				if err != nil {
					reason = "approval_failed"
					result := toolError(fmt.Errorf("approval failed for tool %q: %w", call.Name, err))
					emitToolFinish(onEvent, call, summary, result)
					return nil, "", &result
				}
				if !decision.Allow {
					reason = "approval_denied"
					result := toolError(fmt.Errorf("tool call %q denied by user", call.Name))
					emitToolFinish(onEvent, call, summary, result)
					return nil, "", &result
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		reason = runlog.Outcome(err)
		result := toolError(fmt.Errorf("tool not executed: %w", err))
		emitToolFinish(onEvent, call, summary, result)
		runlog.Logger(ctx, slog.Default()).DebugContext(ctx, "tool execution skipped", "tool", call.Name, "toolCallID", call.ID, "outcome", runlog.Outcome(err))
		return nil, "", &result
	}
	return tool, summary, nil
}

// startPreparedCall runs an approved tool call unless ctx is already done:
// executions that have not started yet never start after cancellation.
// In-flight executions observe cancellation through their own ctx.
func (r *Runner) startPreparedCall(ctx context.Context, tool Tool, call ToolCall, summary string, onEvent func(Event)) ToolResult {
	if err := ctx.Err(); err != nil {
		result := toolError(fmt.Errorf("tool not executed: %w", err))
		emitToolFinish(onEvent, call, summary, result)
		runlog.Logger(ctx, slog.Default()).DebugContext(ctx, "tool execution skipped", "tool", call.Name, "toolCallID", call.ID, "outcome", runlog.Outcome(err))
		return result
	}
	return r.executePreparedCall(ctx, tool, call, summary, onEvent)
}

// executePreparedCall runs an approved tool call and emits its finish event.
func (r *Runner) executePreparedCall(ctx context.Context, tool Tool, call ToolCall, summary string, onEvent func(Event)) ToolResult {
	logger := runlog.Logger(ctx, slog.Default()).With("tool", call.Name, "toolCallID", call.ID)
	ctx = runlog.WithLogger(ctx, logger)
	started := time.Now()
	logger.DebugContext(ctx, "tool execution started")
	result := func() (result ToolResult) {
		defer func() {
			if value := recover(); value != nil {
				logger.Error("tool panicked", "panic", value, "stack", string(debug.Stack()))
				result = toolError(fmt.Errorf("tool %q panicked: %v", call.Name, value))
			}
		}()
		return tool.Execute(ctx, call.Arguments)
	}()
	outcome := "success"
	if result.IsError {
		outcome = "failed"
	}
	logger.DebugContext(ctx, "tool execution finished", "durationMs", float64(time.Since(started).Microseconds())/1000, "outcome", outcome, "promptTokens", result.Usage.PromptTokens, "completionTokens", result.Usage.CompletionTokens, "totalTokens", result.Usage.TotalTokens)
	emitToolFinish(onEvent, call, summary, result)
	return result
}

func toolError(err error) ToolResult {
	return ToolResult{
		IsError: true,
		Content: []ContentBlock{{
			Type: ContentText,
			Text: err.Error(),
		}},
	}
}

func describeToolCall(tool Tool, call ToolCall) (ApprovalRequest, error) {
	if describer, ok := tool.(ApprovalDescriber); ok {
		req, err := describer.ApprovalRequest(call.Arguments)
		if req.ToolName == "" {
			req.ToolName = call.Name
		}
		if req.Summary == "" {
			req.Summary, req.Details = fallbackToolSummary(call)
		}
		if len(req.Arguments) == 0 {
			req.Arguments = call.Arguments
		}
		return req, err
	}
	summary, details := fallbackToolSummary(call)
	return ApprovalRequest{ToolName: call.Name, Summary: summary, Details: details, Arguments: call.Arguments}, nil
}

func fallbackToolSummary(call ToolCall) (string, string) {
	details := strings.TrimSpace(string(call.Arguments))
	if details == "" {
		return call.Name, ""
	}
	return call.Name + " " + truncate(details, 120), details
}

func emitToolFinish(onEvent func(Event), call ToolCall, summary string, result ToolResult) {
	emitToolEvent(onEvent, Event{
		Type:       EventToolCallFinish,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Summary:    summary,
		Details:    resultText(result),
		IsError:    result.IsError,
	})
}

func emitToolEvent(onEvent func(Event), event Event) {
	if onEvent != nil {
		onEvent(event)
	}
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func resultText(result ToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	parts := make([]string, 0, len(result.Content))
	for _, block := range result.Content {
		if block.Type == ContentText {
			parts = append(parts, block.Text)
		}
	}
	if len(parts) == 0 {
		raw, err := json.Marshal(result.Content)
		if err == nil {
			return string(raw)
		}
	}
	return strings.Join(parts, "\n")
}
