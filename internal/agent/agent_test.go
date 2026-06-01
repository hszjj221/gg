package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	runs  int
}

func (t fakeTool) Name() string { return t.name }
func (t fakeTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Description: "fake", Parameters: map[string]any{"type": "object"}}
}
func (t fakeTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: `{"ok":true}`}}, Usage: t.usage}
}

type fakeApprovalTool struct {
	name        string
	runs        int
	approvalErr error
}

func (t *fakeApprovalTool) Name() string { return t.name }
func (t *fakeApprovalTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Description: "fake", Parameters: map[string]any{"type": "object"}}
}
func (t *fakeApprovalTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	t.runs++
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: `{"ok":true}`}}}
}
func (t *fakeApprovalTool) ApprovalRequest(args json.RawMessage) (ApprovalRequest, error) {
	if t.approvalErr != nil {
		return ApprovalRequest{}, t.approvalErr
	}
	return ApprovalRequest{ToolName: t.name, Summary: "run " + t.name, Details: string(args), Arguments: args}, nil
}

type fakeApprover struct {
	allow    bool
	err      error
	requests []ApprovalRequest
}

func (a *fakeApprover) Approve(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	a.requests = append(a.requests, req)
	if a.err != nil {
		return ApprovalDecision{}, a.err
	}
	return ApprovalDecision{Allow: a.allow}, nil
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

func TestRunnerApprovesDescribedToolBeforeExecution(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{
		toolUseMessage("call-1", "bash", `{"command":"printf ok"}`),
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	tool := &fakeApprovalTool{name: "bash"}
	approver := &fakeApprover{allow: true}
	runner := NewRunnerWithOptions(provider, []Tool{tool}, RunnerOptions{Approver: approver})

	msg, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "run"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Content != "done" || tool.runs != 1 {
		t.Fatalf("approved tool did not run correctly: msg=%q runs=%d", msg.Content, tool.runs)
	}
	if len(approver.requests) != 1 || approver.requests[0].ToolName != "bash" {
		t.Fatalf("approval not requested: %+v", approver.requests)
	}
}

func TestRunnerDeniesDescribedToolWithoutExecution(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{
		toolUseMessage("call-1", "bash", `{"command":"printf ok"}`),
		{Message: Message{Role: RoleAssistant, Content: "blocked"}, StopReason: StopReasonEndTurn},
	}}
	tool := &fakeApprovalTool{name: "bash"}
	runner := NewRunnerWithOptions(provider, []Tool{tool}, RunnerOptions{Approver: &fakeApprover{allow: false}})

	_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "run"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if tool.runs != 0 {
		t.Fatalf("denied tool should not run, runs=%d", tool.runs)
	}
	second := provider.requests[1]
	last := second.Messages[len(second.Messages)-1]
	if last.Role != RoleTool || !strings.Contains(last.Content, `tool call "bash" denied by user`) {
		t.Fatalf("denied result not sent to model: %+v", last)
	}
}

func TestRunnerSkipsApprovalForUndescribedTool(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{
		toolUseMessage("call-1", "read", `{"path":"README.md"}`),
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	approver := &fakeApprover{allow: false}
	runner := NewRunnerWithOptions(provider, []Tool{fakeTool{name: "read"}}, RunnerOptions{Approver: approver})

	if _, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "read"}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(approver.requests) != 0 {
		t.Fatalf("read tool should not require approval: %+v", approver.requests)
	}
}

func TestRunnerReturnsApprovalErrorsAsToolResults(t *testing.T) {
	provider := &fakeProvider{responses: []AssistantMessage{
		toolUseMessage("call-1", "bash", `{"command":"printf ok"}`),
		{Message: Message{Role: RoleAssistant, Content: "blocked"}, StopReason: StopReasonEndTurn},
	}}
	tool := &fakeApprovalTool{name: "bash"}
	runner := NewRunnerWithOptions(provider, []Tool{tool}, RunnerOptions{Approver: &fakeApprover{err: errors.New("approval unavailable")}})

	_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "run"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if tool.runs != 0 {
		t.Fatalf("tool should not run after approval error, runs=%d", tool.runs)
	}
	second := provider.requests[1]
	last := second.Messages[len(second.Messages)-1]
	if !strings.Contains(last.Content, "approval failed for tool \"bash\"") || !strings.Contains(last.Content, "approval unavailable") {
		t.Fatalf("approval error not sent to model: %+v", last)
	}
}

func toolUseMessage(id, name, args string) AssistantMessage {
	return AssistantMessage{
		Message: Message{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:        id,
				Name:      name,
				Arguments: json.RawMessage(args),
			}},
		},
		StopReason: StopReasonToolUse,
	}
}
