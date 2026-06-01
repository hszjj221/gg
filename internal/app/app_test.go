package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
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

type appToolProvider struct {
	requests []agent.Request
	command  string
}

func (p *appToolProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) == 1 {
		return agent.AssistantMessage{
			Message: agent.Message{
				Role: agent.RoleAssistant,
				ToolCalls: []agent.ToolCall{{
					ID:        "call-1",
					Name:      "bash",
					Arguments: []byte(`{"command":` + strconv.Quote(p.command) + `}`),
				}},
			},
			StopReason: agent.StopReasonToolUse,
		}, nil
	}
	if onEvent != nil {
		onEvent(agent.Event{Type: agent.EventTextDelta, Text: "done"})
	}
	return agent.AssistantMessage{
		Message:    agent.Message{Role: agent.RoleAssistant, Content: "done", ContentBlocks: []agent.ContentBlock{{Type: agent.ContentText, Text: "done"}}},
		StopReason: agent.StopReasonEndTurn,
	}, nil
}

func TestRunPrintModeOutputsFinalTextAndWritesSession(t *testing.T) {
	dir := t.TempDir()
	sessionPath := dir + "/session.jsonl"
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--session", sessionPath, "say hi"}, Options{
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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
	if len(loaded.Models) != 1 || loaded.Models[0].Selection != "openai:gpt-4.1" {
		t.Fatalf("initial model was not persisted: %+v", loaded.Models)
	}
}

func TestRunPrintModeUsageWritesStderrAndSession(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{usage: agent.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}}

	code := Run(context.Background(), []string{"-p", "--usage", "--api-key", "key", "--session", sessionPath, "say hi"}, Options{
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
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

func TestRunPrintModeAutoApprovalDoesNotPrompt(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	var stdout, stderr strings.Builder
	provider := &appToolProvider{command: "printf ok > " + strconv.Quote(marker)}

	code := Run(context.Background(), []string{"-p", "--api-key", "key", "--no-session", "write marker"}, Options{
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("auto print mode should execute without approval prompt: %v", err)
	}
	if strings.Contains(stderr.String(), "Approve tool call") {
		t.Fatalf("auto print mode should not prompt: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "tool ") || strings.Contains(stderr.String(), "tool ") {
		t.Fatalf("print mode should not render tool logs: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestTurnExecutorForwardsToolEvents(t *testing.T) {
	dir := t.TempDir()
	provider := &appToolProvider{command: "printf ok"}
	executor := newTurnExecutor(
		config.Config{CWD: dir, Selection: "openai:gpt-4.1"},
		func(config.Config) agent.Provider { return provider },
		nil,
		nil,
		skills.Set{},
		true,
	)
	var events []agent.Event

	_, err := executor.Run(context.Background(), "run command", func(event agent.Event) {
		events = append(events, event)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(events) < 3 {
		t.Fatalf("expected text and tool events, got %+v", events)
	}
	if events[0].Type != agent.EventToolCallStart || events[0].ToolName != "bash" {
		t.Fatalf("missing tool start event: %+v", events)
	}
	if events[1].Type != agent.EventToolCallFinish || events[1].ToolName != "bash" || events[1].IsError {
		t.Fatalf("missing tool finish event: %+v", events)
	}
	if events[2].Type != agent.EventTextDelta || events[2].Text != "done" {
		t.Fatalf("missing final text delta: %+v", events)
	}
}

func TestRunApprovalOnRequestRequiresTerminal(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	providerCalled := false

	code := Run(context.Background(), []string{"-p", "--approval", "on-request", "--api-key", "key", "--no-session", "write marker"}, Options{
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			providerCalled = true
			return &appFakeProvider{}
		},
	})

	if code == 0 {
		t.Fatalf("expected on-request without terminal to fail")
	}
	if providerCalled {
		t.Fatalf("provider should not be called when approval cannot prompt")
	}
	if !strings.Contains(stderr.String(), "--approval on-request requires a terminal") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunApprovalOnRequestAllowsToolInPromptMode(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	var stdout, stderr strings.Builder
	provider := &appToolProvider{command: "printf ok > " + strconv.Quote(marker)}

	code := Run(context.Background(), []string{"-p", "--approval", "on-request", "--api-key", "key", "--no-session", "write marker"}, Options{
		CWD:        dir,
		HomeDir:    filepath.Join(dir, "home"),
		Stdin:      strings.NewReader("y\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		IsTerminal: func(any) bool { return true },
		ProviderFactory: func(config.Config) agent.Provider {
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("approved tool should run: %v", err)
	}
	if !strings.Contains(stderr.String(), "Approve tool call") || !strings.Contains(stderr.String(), "bash") {
		t.Fatalf("approval prompt not written to stderr: %q", stderr.String())
	}
}

func TestRunApprovalOnRequestDeniesToolInPromptMode(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	var stdout, stderr strings.Builder
	provider := &appToolProvider{command: "printf ok > " + strconv.Quote(marker)}

	code := Run(context.Background(), []string{"-p", "--approval", "on-request", "--api-key", "key", "--no-session", "write marker"}, Options{
		CWD:        dir,
		HomeDir:    filepath.Join(dir, "home"),
		Stdin:      strings.NewReader("n\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		IsTerminal: func(any) bool { return true },
		ProviderFactory: func(config.Config) agent.Provider {
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("denied tool should not create marker, stat err=%v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("model should receive denied tool result and continue, requests=%d", len(provider.requests))
	}
	last := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if !strings.Contains(last.Content, `tool call "bash" denied by user`) {
		t.Fatalf("denied tool result not sent to model: %+v", last)
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
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
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
	if len(messages) != 2 || messages[0].Content != "previous" || messages[1].Content != "next" {
		t.Fatalf("latest session was not loaded: %+v", messages)
	}
}

func TestRunWithoutPromptUsesLineInteractiveWhenNotTTY(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}

	code := Run(context.Background(), []string{"--api-key", "key", "--no-session"}, Options{
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdin:   strings.NewReader("say hi\n"),
		Stdout:  &stdout,
		Stderr:  &stderr,
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

func TestLineInteractiveModelCommandSwitchesProviderModel(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	writeAppConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1", "gpt-4.1-mini"]
    },
    "local": {
      "type": "openai-compatible",
      "baseURL": "http://localhost:11434/v1",
      "apiKey": "ollama",
      "models": ["qwen2.5-coder"]
    }
  }
}`)
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}
	var configs []config.Config

	code := Run(context.Background(), []string{"--no-skills", "--no-session"}, Options{
		CWD:     dir,
		HomeDir: home,
		Stdin:   strings.NewReader("/model local:qwen2.5-coder\nsay hi\n"),
		Stdout:  &stdout,
		Stderr:  &stderr,
		ProviderFactory: func(cfg config.Config) agent.Provider {
			configs = append(configs, cfg)
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected one provider request after model command, got %d", len(provider.requests))
	}
	if len(configs) != 1 || configs[0].Selection != "local:qwen2.5-coder" || configs[0].BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("provider did not use switched config: %+v", configs)
	}
	if !strings.Contains(stdout.String(), "model switched to local:qwen2.5-coder") {
		t.Fatalf("missing switch output: %q", stdout.String())
	}
}

func TestModelCommandMissingSelectionDoesNotCallProvider(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	writeAppConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1"]
    }
  }
}`)
	var stdout, stderr strings.Builder
	providerCalled := false

	code := Run(context.Background(), []string{"-p", "--no-skills", "--no-session", "/model", "missing:model"}, Options{
		CWD:     dir,
		HomeDir: home,
		Stdout:  &stdout,
		Stderr:  &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			providerCalled = true
			return &appFakeProvider{}
		},
	})

	if code == 0 {
		t.Fatalf("expected missing model to fail")
	}
	if providerCalled {
		t.Fatalf("provider should not be called for missing model")
	}
	if !strings.Contains(stderr.String(), `unknown provider "missing"`) {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestModelCommandListsSelectionsWithoutCallingProviderOrWritingMessages(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	sessionPath := filepath.Join(dir, "session.jsonl")
	writeAppConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1", "gpt-4.1-mini"]
    }
  }
}`)
	var stdout, stderr strings.Builder
	providerCalled := false

	code := Run(context.Background(), []string{"-p", "--no-skills", "--session", sessionPath, "/model"}, Options{
		CWD:     dir,
		HomeDir: home,
		Stdout:  &stdout,
		Stderr:  &stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			providerCalled = true
			return &appFakeProvider{}
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if providerCalled {
		t.Fatalf("provider should not be called for /model list")
	}
	if !strings.Contains(stdout.String(), "current model: openai:gpt-4.1") || !strings.Contains(stdout.String(), "- openai:gpt-4.1-mini") {
		t.Fatalf("unexpected /model output: %q", stdout.String())
	}
	loaded, err := session.Load(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 0 {
		t.Fatalf("/model list should not write messages: %+v", loaded.Messages)
	}
}

func TestResumeRestoresLastModelSelection(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	writeAppConfig(t, home, `{
  "default": "openai:gpt-4.1",
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "openai-key",
      "models": ["gpt-4.1", "gpt-4.1-mini"]
    }
  }
}`)
	sessionDir := filepath.Join(dir, "sessions")
	sessionPath := filepath.Join(session.CWDDir(sessionDir, dir), "session.jsonl")
	store, err := session.NewStore(sessionPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendModel("openai", "gpt-4.1-mini"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "previous"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	provider := &appFakeProvider{}
	var configs []config.Config

	code := Run(context.Background(), []string{"--continue", "--no-skills", "--session-dir", sessionDir, "next"}, Options{
		CWD:     dir,
		HomeDir: home,
		Stdout:  &stdout,
		Stderr:  &stderr,
		ProviderFactory: func(cfg config.Config) agent.Provider {
			configs = append(configs, cfg)
			return provider
		},
	})

	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if len(configs) != 1 || configs[0].Selection != "openai:gpt-4.1-mini" {
		t.Fatalf("resume did not restore model selection: %+v", configs)
	}
}

func writeAppConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".gg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
