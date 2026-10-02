package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

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

func TestWriteToolRejectsSymlinkParentEscapeFromCWD(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result := executeTool(t, NewWriteTool(dir), `{"path":"outside/file.txt","content":"secret"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("write escaped cwd through symlink")
	}
}

func TestWriteToolApprovalRequestPreviewsCreateAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	create, err := tool.ApprovalRequest(json.RawMessage(`{"path":"new.txt","content":"new content\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if create.ToolName != "write" || !strings.Contains(create.Summary, "create new.txt") || !strings.Contains(create.Details, "new content") {
		t.Fatalf("unexpected create approval request: %+v", create)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("old content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overwrite, err := tool.ApprovalRequest(json.RawMessage(`{"path":"new.txt","content":"new content\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"overwrite new.txt", "old content", "new content"} {
		if !strings.Contains(overwrite.Summary+"\n"+overwrite.Details, want) {
			t.Fatalf("overwrite approval missing %q: %+v", want, overwrite)
		}
	}
}

func TestWriteToolApprovalRequestShowsOverwriteChangeAfterLongCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	prefix := strings.Repeat("x", approvalPreviewLimit+100)
	if err := os.WriteFile(path, []byte(prefix+"\nold tail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteTool(dir)

	req, err := tool.ApprovalRequest(json.RawMessage(`{"path":"file.txt","content":"` + prefix + `\nnew tail\n"}`))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"old tail", "new tail"} {
		if !strings.Contains(req.Details, want) {
			t.Fatalf("approval preview should show changed tail %q:\n%s", want, req.Details)
		}
	}
}

func TestWriteToolApprovalRequestPreApprovedInAgentDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	inside, err := tool.ApprovalRequest(json.RawMessage(`{"path":".gg/agent/draft.txt","content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !inside.PreApproved {
		t.Fatalf("write inside .gg/agent must be pre-approved: %+v", inside)
	}
	if inside.PreApprovedReason == "" {
		t.Fatalf("pre-approved write must carry a reason: %+v", inside)
	}

	// Boundary: a sibling directory whose name merely starts with "agent"
	// must not be exempted.
	for _, path := range []string{"notes.txt", ".gg/agent-evil/draft.txt", ".gg/other/draft.txt"} {
		req, err := tool.ApprovalRequest(json.RawMessage(`{"path":` + strconv.Quote(path) + `,"content":"hello"}`))
		if err != nil {
			t.Fatal(err)
		}
		if req.PreApproved {
			t.Fatalf("write to %q must not be pre-approved", path)
		}
	}
}

func TestWriteToolExecuteCreatesAgentDirLazily(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	result := executeTool(t, tool, `{"path":".gg/agent/deep/draft.txt","content":"hello"}`)
	if result.IsError {
		t.Fatalf("expected write success: %s", result.Content[0].Text)
	}
	content, err := os.ReadFile(filepath.Join(dir, ".gg", "agent", "deep", "draft.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
	info, err := os.Stat(filepath.Join(dir, ".gg", "agent"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("agent dir has permissions %04o, want 0700", perm)
	}
}

// scriptedProvider replays canned assistant messages for runner tests.
type scriptedProvider struct {
	responses []agent.AssistantMessage
}

func (p *scriptedProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	if len(p.responses) == 0 {
		return agent.AssistantMessage{}, errNoScriptedResponse
	}
	msg := p.responses[0]
	p.responses = p.responses[1:]
	return msg, nil
}

var errNoScriptedResponse = errors.New("no scripted provider response")

// recordingApprover records every approval request it sees.
type recordingApprover struct {
	allow    bool
	requests []agent.ApprovalRequest
}

func (a *recordingApprover) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	a.requests = append(a.requests, req)
	return agent.ApprovalDecision{Allow: a.allow}, nil
}

func runSingleWriteCall(t *testing.T, dir, argsJSON string) (*recordingApprover, []agent.Event) {
	t.Helper()
	provider := &scriptedProvider{responses: []agent.AssistantMessage{
		{
			Message: agent.Message{
				Role: agent.RoleAssistant,
				ToolCalls: []agent.ToolCall{{
					ID:        "call-1",
					Name:      "write",
					Arguments: json.RawMessage(argsJSON),
				}},
			},
			StopReason: agent.StopReasonToolUse,
		},
		{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: "done"},
			StopReason: agent.StopReasonEndTurn,
		},
	}}
	approver := &recordingApprover{allow: true}
	var events []agent.Event
	runner := agent.NewRunnerWithOptions(
		provider,
		[]agent.Tool{NewWriteTool(dir)},
		agent.RunnerOptions{Approver: approver},
	)
	if _, err := runner.Run(context.Background(),
		[]agent.Message{{Role: agent.RoleUser, Content: "write it"}},
		func(e agent.Event) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}
	return approver, events
}

func findEvent(events []agent.Event, typ agent.EventType) *agent.Event {
	for i := range events {
		if events[i].Type == typ {
			return &events[i]
		}
	}
	return nil
}

func TestRunnerSkipsApproverForAgentAreaWrite(t *testing.T) {
	dir := t.TempDir()
	approver, events := runSingleWriteCall(t, dir, `{"path":".gg/agent/draft.txt","content":"hello"}`)

	if len(approver.requests) != 0 {
		t.Fatalf("approver was called %d times for an agent-area write, want 0", len(approver.requests))
	}
	// The write still executed.
	content, err := os.ReadFile(filepath.Join(dir, ".gg", "agent", "draft.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
	// Start and finish events are still emitted, with the exemption
	// annotated for transparency.
	start := findEvent(events, agent.EventToolCallStart)
	finish := findEvent(events, agent.EventToolCallFinish)
	if start == nil || finish == nil {
		t.Fatalf("expected tool_call_start and tool_call_finish events, got %v", events)
	}
	if !strings.Contains(start.Summary, "[agent area]") {
		t.Fatalf("start event summary should note the agent-area exemption: %q", start.Summary)
	}
	if !strings.Contains(finish.Summary, "[agent area]") {
		t.Fatalf("finish event summary should note the agent-area exemption: %q", finish.Summary)
	}
	if finish.IsError {
		t.Fatalf("finish event reports error: %+v", finish)
	}
}

func TestRunnerStillApprovesWriteOutsideAgentDir(t *testing.T) {
	dir := t.TempDir()
	approver, events := runSingleWriteCall(t, dir, `{"path":"notes.txt","content":"hello"}`)

	if len(approver.requests) != 1 {
		t.Fatalf("approver was called %d times for a non-agent-area write, want 1", len(approver.requests))
	}
	if approver.requests[0].PreApproved {
		t.Fatalf("non-agent-area write must not be pre-approved: %+v", approver.requests[0])
	}
	start := findEvent(events, agent.EventToolCallStart)
	if start == nil {
		t.Fatalf("expected a tool_call_start event, got %v", events)
	}
	if strings.Contains(start.Summary, "[agent area]") {
		t.Fatalf("non-agent-area write must not be annotated as agent area: %q", start.Summary)
	}
	content, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}
