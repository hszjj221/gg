package tools

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

type fakeSubagentProvider struct {
	requests []agent.Request
	response agent.AssistantMessage
	err      error
}

func (p *fakeSubagentProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return agent.AssistantMessage{}, p.err
	}
	return p.response, nil
}

func TestSubagentToolReturnsChildAgentFinalTextAndReadOnlyTools(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSubagentProvider{response: agent.AssistantMessage{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: "checked README.md: ok"},
		StopReason: agent.StopReasonEndTurn,
		Usage:      agent.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
	}}
	tool := NewSubagentTool(dir, provider, SubagentOptions{})

	result := executeTool(t, tool, `{"task":"inspect docs","context":"focus README","maxTurns":3}`)

	if result.IsError {
		t.Fatalf("expected success: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "checked README.md: ok" {
		t.Fatalf("unexpected subagent result: %q", got)
	}
	if result.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected subagent usage: %+v", result.Usage)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one child request, got %d", len(provider.requests))
	}
	if provider.requests[0].Messages[0].Role != agent.RoleSystem {
		t.Fatalf("expected system message first: %+v", provider.requests[0].Messages)
	}
	names := toolNames(provider.requests[0].Tools)
	if got, want := strings.Join(names, ","), "grep,list,read"; got != want {
		t.Fatalf("unexpected child tools: want %s, got %s", want, got)
	}
}

func TestSubagentToolReturnsProviderErrorsAsToolErrors(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeSubagentProvider{err: errors.New("provider failed")}
	tool := NewSubagentTool(dir, provider, SubagentOptions{})

	result := executeTool(t, tool, `{"task":"inspect docs"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "provider failed") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func toolNames(defs []agent.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	sort.Strings(names)
	return names
}
