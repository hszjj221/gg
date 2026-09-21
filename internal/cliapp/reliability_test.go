package cliapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/contextmgr"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
)

type failAfterWriteProvider struct{ calls int }

func (p *failAfterWriteProvider) Complete(ctx context.Context, req agent.Request, event func(agent.Event)) (agent.AssistantMessage, error) {
	p.calls++
	if p.calls > 1 {
		return agent.AssistantMessage{}, errors.New("simulated provider failure")
	}
	return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "w1", Name: "write", Arguments: []byte(`{"path":"changed.txt","content":"already changed"}`)}}}, StopReason: agent.StopReasonToolUse}, nil
}

func TestFailedBatchedCompactionLeavesValidToolBoundary(t *testing.T) {
	p := &appContextProvider{summary: "earlier user goal"}
	history := []agent.Message{
		{Role: agent.RoleUser, Content: strings.Repeat("x", 2000)},
		{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "r1", Name: "read", Arguments: []byte(`{"path":"big.txt"}`)}}},
		{Role: agent.RoleTool, ToolCallID: "r1", Content: strings.Repeat("y", 5000)},
		{Role: agent.RoleUser, Content: "continue"},
	}
	cfg := config.Config{CWD: t.TempDir(), Context: config.ContextConfig{MaxPromptTokens: 1000, SummaryMaxTokens: 100, TailTurns: 1}}
	e := newTurnExecutor(cfg, func(config.Config) agent.Provider { return p }, nil, history, nil, skills.Set{}, true)
	_, err := e.Run(context.Background(), "/compact", nil, nil)
	if err == nil {
		t.Fatal("oversized tool batch should fail without being split")
	}
	snapshot := e.Snapshot()
	if snapshot.SummaryThrough != 1 {
		t.Fatalf("partial compaction committed an invalid boundary: %+v", snapshot)
	}
	build := contextmgr.Build(contextmgr.BuildInput{History: snapshot.Messages, Summary: contextmgr.SummaryState{Text: snapshot.Summary, ThroughMessageCount: snapshot.SummaryThrough}, Config: cfg.Context})
	if build.Messages[1].Role != agent.RoleAssistant || len(build.Messages[1].ToolCalls) != 1 || build.Messages[2].Role != agent.RoleTool {
		t.Fatal("retained tool sequence was corrupted")
	}
}

type longTurnProvider struct {
	mainCalls, summaries int
	requests             []agent.Request
}

func (p *longTurnProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	p.requests = append(p.requests, req)
	if len(req.Tools) == 0 {
		p.summaries++
		return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "Preserve the user's constraint; blob.txt was read."}, StopReason: agent.StopReasonEndTurn}, nil
	}
	p.mainCalls++
	if p.mainCalls <= 2 {
		return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: fmt.Sprint(p.mainCalls), Name: "read", Arguments: []byte(`{"path":"blob.txt"}`)}}}, StopReason: agent.StopReasonToolUse}, nil
	}
	return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "done"}, StopReason: agent.StopReasonEndTurn}, nil
}

func TestCompactionRunsBetweenToolBatchesWithoutLosingTranscript(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob.txt"), []byte(strings.Repeat("x", 2400)), 0600); err != nil {
		t.Fatal(err)
	}
	p := &longTurnProvider{}
	cfg := config.Config{CWD: dir, Context: config.ContextConfig{MaxPromptTokens: 2000, MaxOutputTokens: 400, TailTurns: 6, SummaryMaxTokens: 100, AutoCompact: true}}
	e := newTurnExecutor(cfg, func(config.Config) agent.Provider { return p }, nil, nil, nil, skills.Set{}, true)
	result, err := e.Run(context.Background(), "Preserve my constraint and inspect the file", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" || p.summaries == 0 || p.mainCalls != 3 {
		t.Fatalf("did not compact inside turn: %+v result=%+v", p, result)
	}
	for _, req := range p.requests {
		if tokens := contextmgr.EstimateMessages(req.Messages) + contextmgr.EstimateTools(req.Tools); tokens > 2000 {
			t.Fatalf("sent oversized request: %d", tokens)
		}
	}
	snapshot := e.Snapshot()
	if len(snapshot.Messages) != 6 {
		t.Fatalf("original transcript was changed: %d messages", len(snapshot.Messages))
	}
	if snapshot.Summary == "" || snapshot.Messages[snapshot.SummaryThrough].Role == agent.RoleTool {
		t.Fatal("compaction cut a tool batch")
	}
}

func TestResumeRepairsMissingToolResultsWithoutReplaying(t *testing.T) {
	dir := t.TempDir()
	p := &appFakeProvider{}
	history := []agent.Message{{Role: agent.RoleUser, Content: "write"}, {Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "w1", Name: "write", Arguments: []byte(`{"path":"should-not-exist","content":"x"}`)}}}}
	e := newTurnExecutor(config.Config{CWD: dir}, func(config.Config) agent.Provider { return p }, nil, history, nil, skills.Set{}, true)
	if _, err := e.Run(context.Background(), "continue", nil, nil); err != nil {
		t.Fatal(err)
	}
	history = e.Snapshot().Messages
	if history[2].Role != agent.RoleTool || history[2].ToolCallID != "w1" || !strings.Contains(history[2].Content, "may already have run") {
		t.Fatalf("missing recovery result: %+v", history)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("tool was replayed: %v", err)
	}
}

func TestBudgetRejectsUncompressiblePromptBeforeProviderCall(t *testing.T) {
	p := &appFakeProvider{}
	e := newTurnExecutor(config.Config{CWD: t.TempDir(), Context: config.ContextConfig{MaxPromptTokens: 100, AutoCompact: true}}, func(config.Config) agent.Provider { return p }, nil, nil, nil, skills.Set{}, true)
	if _, err := e.Run(context.Background(), strings.Repeat("large", 1000), nil, nil); err == nil {
		t.Fatal("oversized request accepted")
	}
	if len(p.requests) != 0 {
		t.Fatal("oversized request sent to provider")
	}
	if len(e.Snapshot().Messages) != 1 {
		t.Fatal("user input should remain recoverable")
	}
}

func TestProjectInstructionsReloadAndCanBeDisabled(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	os.MkdirAll(child, 0700)
	for path, text := range map[string]string{filepath.Join(parent, "AGENTS.md"): "parent convention", filepath.Join(child, "AGENTS.md"): "child convention"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	provider := &appContextProvider{}
	e := newTurnExecutor(config.Config{CWD: child}, func(config.Config) agent.Provider { return provider }, nil, nil, nil, skills.Set{}, true)
	if _, err := e.Run(context.Background(), "first", nil, nil); err != nil {
		t.Fatal(err)
	}
	text := provider.requests[0].Messages[0].Content
	if strings.Index(text, "parent convention") < 0 || strings.Index(text, "parent convention") > strings.Index(text, "child convention") {
		t.Fatalf("wrong instruction order: %s", text)
	}
	if err := os.WriteFile(filepath.Join(child, "AGENTS.md"), []byte("updated convention"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Run(context.Background(), "second", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !containsMessage(provider.requests[len(provider.requests)-1].Messages, "updated convention") {
		t.Fatal("rules were not reloaded")
	}
	disabledProvider := &appContextProvider{}
	e = newTurnExecutor(config.Config{CWD: child, NoContextFiles: true}, func(config.Config) agent.Provider { return disabledProvider }, nil, nil, nil, skills.Set{}, true)
	if _, err := e.Run(context.Background(), "disabled", nil, nil); err != nil {
		t.Fatal(err)
	}
	messages := disabledProvider.requests[0].Messages
	if containsMessage(messages, "convention") || !containsMessage(messages, "coding agent") {
		t.Fatal("disable flag should retain only base instructions")
	}
}
func TestPreserveExecutedWorkAfterProviderFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	p := &failAfterWriteProvider{}
	code := Run(context.Background(), []string{"--api-key", "test", "--no-skills", "--no-memory", "--session", path, "-p", "write a file"}, Options{CWD: dir, HomeDir: filepath.Join(dir, "home"), Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, ProviderFactory: func(config.Config) agent.Provider { return p }})
	if code != 1 {
		t.Fatalf("expected simulated failure, exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "changed.txt")); err != nil {
		t.Fatal(err)
	}
	loaded, err := session.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) < 3 {
		t.Fatalf("file was changed, but session contains %d messages; need user + tool call + result", len(loaded.Messages))
	}
}
