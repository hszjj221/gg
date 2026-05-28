package app

import (
	"context"
	"strings"
	"testing"

	"gg/internal/agent"
	"gg/internal/config"
	"gg/internal/session"
)

type appFakeProvider struct{}

func (appFakeProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
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

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--session", sessionPath, "say hi"}, Options{
		CWD:    dir,
		Stdout: &stdout,
		Stderr: &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			return appFakeProvider{}
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
