package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

type appFakeProvider struct {
	requests []agent.Request
	usage    agent.Usage
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
		Usage:      p.usage,
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

func TestRunPrintModeUsageWritesStderrAndSession(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{usage: agent.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}}

	code := Run(context.Background(), []string{"-p", "--usage", "--api-key", "key", "--session", sessionPath, "say hi"}, Options{
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
	if got := stderr.String(); !strings.Contains(got, "tokens: prompt=7 completion=3 total=10") {
		t.Fatalf("usage not written to stderr: %q", got)
	}
	loaded, err := session.Load(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Usages) != 1 || loaded.Usages[0].Usage.TotalTokens != 10 {
		t.Fatalf("usage was not persisted: %+v", loaded.Usages)
	}
}

func TestRunPrintModeDoesNotPrintUsageByDefault(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{usage: agent.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}}

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
	if strings.Contains(stderr.String(), "tokens:") {
		t.Fatalf("usage should not be written by default: %q", stderr.String())
	}
	loaded, err := session.Load(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Usages) != 1 || loaded.Usages[0].Usage.TotalTokens != 10 {
		t.Fatalf("usage should still be persisted by default: %+v", loaded.Usages)
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

func TestRunInjectsAvailableSkillsSystemMessage(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	project := filepath.Join(dir, "project")
	writeAppSkill(t, filepath.Join(project, ".agents", "skills", "review"), `---
name: review
description: Review local changes.
---

# review
`)
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--no-session", "check this"}, Options{
		CWD:     project,
		HomeDir: home,
		Stdout:  &stdout,
		Stderr:  &stderr,
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
	messages := provider.requests[0].Messages
	if len(messages) < 2 || messages[0].Role != agent.RoleSystem {
		t.Fatalf("expected system message first: %+v", messages)
	}
	if got := messages[0].Content; !strings.Contains(got, "<available_skills>") || !strings.Contains(got, "<name>review</name>") {
		t.Fatalf("system message missing skill list:\n%s", got)
	}
	if got := messages[1].Content; got != "check this" {
		t.Fatalf("unexpected user prompt: %q", got)
	}
}

func TestRunNoSkillsDoesNotInjectSystemMessage(t *testing.T) {
	dir := t.TempDir()
	writeAppSkill(t, filepath.Join(dir, ".agents", "skills", "review"), `---
name: review
description: Review local changes.
---

# review
`)
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"-p", "--no-skills", "--api-key", "key", "--no-session", "check this"}, Options{
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
	messages := provider.requests[0].Messages
	if len(messages) != 1 || messages[0].Role != agent.RoleUser || messages[0].Content != "check this" {
		t.Fatalf("--no-skills should leave only user prompt, got %+v", messages)
	}
}

func TestRunSkillCommandInjectsFullSkillMarkdown(t *testing.T) {
	dir := t.TempDir()
	skillContent := `---
name: ca
description: Review and commit.
---

# ca

Follow review-first workflow.
`
	writeAppSkill(t, filepath.Join(dir, ".agents", "skills", "ca"), skillContent)
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--no-session", "/skill:ca", "commit", "changes"}, Options{
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
	messages := provider.requests[0].Messages
	if len(messages) < 2 {
		t.Fatalf("expected system and user messages: %+v", messages)
	}
	userPrompt := messages[len(messages)-1].Content
	if !strings.Contains(userPrompt, "<skill name=\"ca\"") || !strings.Contains(userPrompt, "Follow review-first workflow.") {
		t.Fatalf("forced skill prompt missing skill markdown:\n%s", userPrompt)
	}
	if !strings.Contains(userPrompt, "commit changes") {
		t.Fatalf("forced skill prompt missing user task:\n%s", userPrompt)
	}
}

func TestRunSkillCommandMissingSkillDoesNotCallProvider(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--no-session", "/skill:missing", "task"}, Options{
		CWD:    dir,
		Stdout: &stdout,
		Stderr: &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			return provider
		},
	})

	if code == 0 {
		t.Fatalf("expected missing skill to fail")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider should not be called when skill is missing")
	}
	if !strings.Contains(stderr.String(), `skill "missing" not found`) {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunSessionsListPrintsSessionsWithoutProvider(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "sessions")
	sessionPath := filepath.Join(session.CWDDir(sessionDir, dir), "session.jsonl")
	store, err := session.NewStore(sessionPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	providerCalled := false

	code := Run(context.Background(), []string{"--session-dir", sessionDir, "sessions", "list"}, Options{
		CWD:    dir,
		Stdout: &stdout,
		Stderr: &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			providerCalled = true
			return &appFakeProvider{}
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if providerCalled {
		t.Fatalf("provider should not be created for sessions list")
	}
	if !strings.Contains(stdout.String(), "session.jsonl") || !strings.Contains(stdout.String(), "hello") {
		t.Fatalf("unexpected sessions list output: %q", stdout.String())
	}
}

func writeAppSkill(t *testing.T, dir string, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunContinueLoadsLatestSession(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "sessions")
	sessionPath := filepath.Join(session.CWDDir(sessionDir, dir), "session.jsonl")
	store, err := session.NewStore(sessionPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "previous"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"--continue", "--no-skills", "--session-dir", sessionDir, "next"}, Options{
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
	messages := provider.requests[0].Messages
	if len(messages) != 2 || messages[0].Content != "previous" || messages[1].Content != "next" {
		t.Fatalf("latest session was not loaded: %+v", messages)
	}
}

func TestRunWithoutPromptUsesLineInteractiveWhenNotTTY(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"--api-key", "key", "--no-session"}, Options{
		CWD:    dir,
		Stdin:  strings.NewReader("say hi\n"),
		Stdout: &stdout,
		Stderr: &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "gg interactive mode") || !strings.Contains(stdout.String(), "hello") {
		t.Fatalf("line interactive fallback not used, stdout: %q", stdout.String())
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(provider.requests))
	}
}
