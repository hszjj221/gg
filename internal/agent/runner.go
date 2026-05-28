package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const maxTurns = 32

type Runner struct {
	provider Provider
	tools    map[string]Tool
	defs     []ToolDefinition

	transcript []Message
}

func NewRunner(provider Provider, tools []Tool) *Runner {
	toolMap := make(map[string]Tool, len(tools))
	defs := make([]ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		toolMap[tool.Name()] = tool
		defs = append(defs, tool.Definition())
	}
	return &Runner{provider: provider, tools: toolMap, defs: defs}
}

func (r *Runner) Transcript() []Message {
	out := make([]Message, len(r.transcript))
	copy(out, r.transcript)
	return out
}

func (r *Runner) Run(ctx context.Context, messages []Message, onEvent func(Event)) (AssistantMessage, error) {
	current := append([]Message(nil), messages...)
	r.transcript = append([]Message(nil), messages...)

	for turn := 0; turn < maxTurns; turn++ {
		reply, err := r.provider.Complete(ctx, Request{Messages: current, Tools: r.defs}, onEvent)
		if err != nil {
			return AssistantMessage{}, err
		}
		current = append(current, reply.Message)
		r.transcript = append(r.transcript, reply.Message)

		if len(reply.ToolCalls) == 0 && reply.StopReason != StopReasonToolUse {
			return reply, nil
		}

		for _, call := range reply.ToolCalls {
			result := r.executeToolCall(ctx, call)
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
			r.transcript = append(r.transcript, toolMessage)
		}
	}

	return AssistantMessage{}, fmt.Errorf("agent exceeded %d turns", maxTurns)
}

func (r *Runner) executeToolCall(ctx context.Context, call ToolCall) ToolResult {
	tool, ok := r.tools[call.Name]
	if !ok {
		return ToolResult{
			IsError: true,
			Content: []ContentBlock{{
				Type: ContentText,
				Text: fmt.Sprintf("unknown tool %q", call.Name),
			}},
		}
	}
	return tool.Execute(ctx, call.Arguments)
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
