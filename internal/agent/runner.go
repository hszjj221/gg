package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const defaultMaxTurns = 32

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

		for _, call := range reply.ToolCalls {
			result := r.executeToolCall(ctx, call, onEvent)
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

func (r *Runner) executeToolCall(ctx context.Context, call ToolCall, onEvent func(Event)) ToolResult {
	if err := ctx.Err(); err != nil {
		result := toolError(fmt.Errorf("tool not executed: %w", err))
		emitToolFinish(onEvent, call, call.Name, result)
		return result
	}
	tool, ok := r.tools[call.Name]
	if !ok {
		summary, details := fallbackToolSummary(call)
		emitToolEvent(onEvent, Event{Type: EventToolCallStart, ToolCallID: call.ID, ToolName: call.Name, Summary: summary, Details: details})
		result := toolError(fmt.Errorf("unknown tool %q", call.Name))
		emitToolFinish(onEvent, call, summary, result)
		return result
	}
	req, reqErr := describeToolCall(tool, call)
	summary := req.Summary
	details := req.Details
	emitToolEvent(onEvent, Event{Type: EventToolCallStart, ToolCallID: call.ID, ToolName: call.Name, Summary: summary, Details: details})
	if reqErr != nil {
		result := toolError(fmt.Errorf("approval request for tool %q failed: %w", call.Name, reqErr))
		emitToolFinish(onEvent, call, summary, result)
		return result
	}
	if r.approver != nil {
		if _, ok := tool.(ApprovalDescriber); ok {
			if req.ToolName == "" {
				req.ToolName = call.Name
			}
			if len(req.Arguments) == 0 {
				req.Arguments = call.Arguments
			}
			decision, err := r.approver.Approve(ctx, req)
			if err != nil {
				result := toolError(fmt.Errorf("approval failed for tool %q: %w", call.Name, err))
				emitToolFinish(onEvent, call, summary, result)
				return result
			}
			if !decision.Allow {
				result := toolError(fmt.Errorf("tool call %q denied by user", call.Name))
				emitToolFinish(onEvent, call, summary, result)
				return result
			}
		}
	}
	if err := ctx.Err(); err != nil {
		result := toolError(fmt.Errorf("tool not executed: %w", err))
		emitToolFinish(onEvent, call, summary, result)
		return result
	}
	result := tool.Execute(ctx, call.Arguments)
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
