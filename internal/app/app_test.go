package app

import (
	"context"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

type appFakeProvider struct {
	requests []agent.Request
}

func (p *appFakeProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	p.requests = append(p.requests, req)
	if onEvent != nil {
		onEvent(agent.Event{Type: agent.EventTextDelta, Text: "hello"})
	}
	return agent.AssistantMessage{
		Message: agent.Message{
			Role:          agent.RoleAssistant,
			Content:       "hello",
			ContentBlocks: []agent.ContentBlock{{Type: agent.ContentText, Text: "hello"}},
		},
		StopReason: agent.StopReasonEndTurn,
	}, nil
}

func TestRunPrintModeOutputsFinalTextAndWritesSession(t *testing.T) {
	dir := t.TempDir()
	sessionPath := dir + "/session.jsonl"
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--session", sessionPath, "say hi"}, Options{
		CWD:    dir,
		Stdout: &stdout,
		Stderr: &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "hello\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	loaded, err := session.Load(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected user and assistant messages, got %d", len(loaded.Messages))
	}
}

func TestRunPrintModeIncludesSubagentTool(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--no-session", "say hi"}, Options{
		CWD:    dir,
		Stdout: &stdout,
		Stderr: &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(provider.requests))
	}
	var found bool
	for _, def := range provider.requests[0].Tools {
		if def.Name == "subagent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("subagent tool missing from request tools: %+v", provider.requests[0].Tools)
	}
}
