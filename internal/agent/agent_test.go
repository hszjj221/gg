package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeProvider struct {
	responses []AssistantMessage
	requests  []Request
}

func (p *fakeProvider) Complete(ctx context.Context, req Request, onEvent func(Event)) (AssistantMessage, error) {
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return AssistantMessage{}, errors.New("no fake responses")
	}
	msg := p.responses[0]
	p.responses = p.responses[1:]
	if onEvent != nil {
		for _, block := range msg.ContentBlocks {
			if block.Type == ContentText {
				onEvent(Event{Type: EventTextDelta, Text: block.Text})
			}
		}
	}
	return msg, nil
}

type fakeTool struct {
	name  string
	usage Usage
}

func (t fakeTool) Name() string { return t.name }
func (t fakeTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Description: "fake", Parameters: map[string]any{"type": "object"}}
}
func (t fakeTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: `{"ok":true}`}}, Usage: t.usage}
}

func TestRunnerReturnsAssistantTextWithoutTools(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{{
		Message:    Message{Role: RoleAssistant, Content: "hello", ContentBlocks: []ContentBlock{{Type: ContentText, Text: "hello"}}},
		StopReason: StopReasonEndTurn,
	}}}
	runner := NewRunner(provider, nil)

	msg, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "say hi"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Content != "hello" {
		t.Fatalf("unexpected assistant content: %q", msg.Content)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one provider call, got %d", len(provider.requests))
	}
}

func TestRunnerExecutesToolCallsAndContinues(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{
		{
			Message: Message{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "read",
					Arguments: json.RawMessage(`{"path":"x"}`),
				}},
			},
			StopReason: StopReasonToolUse,
		},
		{
			Message:    Message{Role: RoleAssistant, Content: "done", ContentBlocks: []ContentBlock{{Type: ContentText, Text: "done"}}},
			StopReason: StopReasonEndTurn,
		},
	}}
	runner := NewRunner(provider, []Tool{fakeTool{name: "read"}})

	msg, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "read x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Content != "done" {
		t.Fatalf("unexpected final response: %q", msg.Content)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(provider.requests))
	}
	second := provider.requests[1]
	if second.Messages[len(second.Messages)-1].Role != RoleTool || second.Messages[len(second.Messages)-1].ToolCallID != "call-1" {
		t.Fatalf("tool result not appended before second call: %+v", second.Messages)
	}
}

func TestRunnerHonorsMaxTurnsOption(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{
		{
			Message: Message{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "read",
					Arguments: json.RawMessage(`{"path":"x"}`),
				}},
			},
			StopReason: StopReasonToolUse,
		},
		{
			Message:    Message{Role: RoleAssistant, Content: "done"},
			StopReason: StopReasonEndTurn,
		},
	}}
	runner := NewRunnerWithOptions(provider, []Tool{fakeTool{name: "read"}}, RunnerOptions{MaxTurns: 1})

	_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "read x"}}, nil)
	if err == nil {
		t.Fatalf("expected max turns error")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one provider call before max turn error, got %d", len(provider.requests))
	}
}

func TestRunnerAccumulatesProviderAndToolUsage(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{
		{
			Message: Message{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "subagent",
					Arguments: json.RawMessage(`{"task":"inspect"}`),
				}},
			},
			StopReason: StopReasonToolUse,
			Usage:      Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
		},
		{
			Message:    Message{Role: RoleAssistant, Content: "done"},
			StopReason: StopReasonEndTurn,
			Usage:      Usage{PromptTokens: 4, CompletionTokens: 1, TotalTokens: 5},
		},
	}}
	runner := NewRunner(provider, []Tool{fakeTool{name: "subagent", usage: Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}}})

	_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "delegate"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	usage := runner.Usage()
	if usage.PromptTokens != 12 || usage.CompletionTokens != 5 || usage.TotalTokens != 17 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}
