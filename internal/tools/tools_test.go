package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

func executeTool(t *testing.T, tool Tool, input string) ToolResult {
	t.Helper()
	result := tool.Execute(context.Background(), json.RawMessage(input))
	if len(result.Content) == 0 {
		t.Fatalf("expected tool result content")
	}
	return result
}

func TestReadToolReadsRequestedLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewReadTool(dir), `{"path":"notes.txt","offset":2,"limit":2}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	if got, want := result.Content[0].Text, "two\nthree"; got != want {
		t.Fatalf("unexpected content:\nwant %q\ngot  %q", want, got)
	}
}

func TestReadToolRejectsPathOutsideCWD(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewReadTool(dir), `{"path":"`+filepath.ToSlash(outside)+`"}`)

	if !result.IsError {
		t.Fatalf("expected error for outside path")
	}
	if !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
}

func TestReadToolAllowsExtraReadOnlyRoots(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "ca")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte("use ca"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadToolWithOptions(dir, ReadOptions{ExtraRoots: []string{skillDir}})
	result := executeTool(t, tool, `{"path":"`+filepath.ToSlash(filepath.Join(skillDir, "references", "guide.md"))+`"}`)

	if result.IsError {
		t.Fatalf("expected extra root read success: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "use ca" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestReadToolRejectsSymlinkEscapeFromExtraReadOnlyRoot(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(t.TempDir(), "ca")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(skillDir, "secret.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	tool := NewReadToolWithOptions(dir, ReadOptions{ExtraRoots: []string{skillDir}})
	result := executeTool(t, tool, `{"path":"`+filepath.ToSlash(link)+`"}`)

	if !result.IsError {
		t.Fatalf("expected symlink escape to be rejected")
	}
	if !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
}

func TestWriteToolCreatesParentDirectoriesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	result := executeTool(t, tool, `{"path":"nested/file.txt","content":"first"}`)
	if result.IsError {
		t.Fatalf("expected write success: %s", result.Content[0].Text)
	}
	result = executeTool(t, tool, `{"path":"nested/file.txt","content":"second"}`)
	if result.IsError {
		t.Fatalf("expected overwrite success: %s", result.Content[0].Text)
	}

	content, err := os.ReadFile(filepath.Join(dir, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "second" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestEditToolRequiresUniqueOldText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewEditTool(dir), `{"path":"main.go","edits":[{"oldText":"beta","newText":"gamma"}]}`)

	if !result.IsError {
		t.Fatalf("expected non-unique oldText to fail")
	}
	if !strings.Contains(result.Content[0].Text, "must match exactly once") {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
}

func TestEditToolAppliesMultipleReplacementsAgainstOriginalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc a() {}\nfunc b() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewEditTool(dir), `{"path":"main.go","edits":[{"oldText":"func a() {}","newText":"func alpha() {}"},{"oldText":"func b() {}","newText":"func beta() {}"}]}`)

	if result.IsError {
		t.Fatalf("expected edit success: %s", result.Content[0].Text)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); !strings.Contains(got, "func alpha() {}") || !strings.Contains(got, "func beta() {}") {
		t.Fatalf("replacements not applied:\n%s", got)
	}
}

func TestBashToolReportsExitCodeAndOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, BashOptions{DefaultTimeout: 5 * time.Second})

	result := executeTool(t, tool, `{"command":"printf hello && exit 7","timeout":5}`)

	if !result.IsError {
		t.Fatalf("expected non-zero exit to be an error")
	}
	if !strings.Contains(result.Content[0].Text, "hello") || !strings.Contains(result.Content[0].Text, "exit code 7") {
		t.Fatalf("unexpected result: %s", result.Content[0].Text)
	}
}

func TestListToolListsDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewListTool(dir), `{"path":"."}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "b.txt\nsubdir/" {
		t.Fatalf("unexpected list output: %q", got)
	}
}

func TestListToolRejectsOutsidePathAndFiles(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideResult := executeTool(t, NewListTool(dir), `{"path":"`+filepath.ToSlash(outside)+`"}`)
	if !outsideResult.IsError || !strings.Contains(outsideResult.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected outside result: %+v", outsideResult)
	}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileResult := executeTool(t, NewListTool(dir), `{"path":"file.txt"}`)
	if !fileResult.IsError || !strings.Contains(fileResult.Content[0].Text, "not a directory") {
		t.Fatalf("unexpected file result: %+v", fileResult)
	}
}

func TestGrepToolFindsMatchesWithPathAndLineNumber(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.txt"), []byte("alpha\nneedle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "b.txt"), []byte("needle again\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":"pkg","pattern":"needle","limit":1}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "pkg/a.txt:2: needle here" {
		t.Fatalf("unexpected grep output: %q", got)
	}
}

func TestGrepToolRejectsOutsidePath(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(outside, []byte("needle"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":"`+filepath.ToSlash(outside)+`","pattern":"needle"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

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
