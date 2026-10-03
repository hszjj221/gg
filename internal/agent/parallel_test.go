package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gateTool blocks in Execute until release is closed, proving that all
// participants started before any finished.
type gateTool struct {
	name    string
	started chan<- string
	release <-chan struct{}
}

func (t *gateTool) ParallelSafe() bool { return true }

func (t *gateTool) Name() string { return t.name }
func (t *gateTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Description: "fake", Parameters: map[string]any{"type": "object"}}
}
func (t *gateTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	t.started <- t.name
	select {
	case <-t.release:
	case <-ctx.Done():
	}
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: `{"ok":true}`}}}
}

func toolCallMessages(names []string) []ToolCall {
	calls := make([]ToolCall, len(names))
	for i, name := range names {
		calls[i] = ToolCall{ID: fmt.Sprintf("call-%d", i), Name: name, Arguments: json.RawMessage(`{}`)}
	}
	return calls
}

func TestToolCallsExecuteInParallel(t *testing.T) {
	const n = 4 // == maxParallelToolCalls, so no semaphore deadlock in the test
	names := []string{"tool-0", "tool-1", "tool-2", "tool-3"}
	started := make(chan string, n)
	release := make(chan struct{})
	tools := make([]Tool, 0, n)
	for _, name := range names {
		tools = append(tools, &gateTool{name: name, started: started, release: release})
	}
	provider := &fakeProvider{responses: []AssistantMessage{
		{Message: Message{Role: RoleAssistant, ToolCalls: toolCallMessages(names)}, StopReason: StopReasonToolUse},
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	runner := NewRunner(provider, tools)

	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil)
		done <- err
	}()

	// All four must start before any finishes. Sequential execution would
	// deadlock here because release never closes until every tool started.
	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("tool calls did not start in parallel")
		}
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish after release")
	}
}

// concurrencyTool tracks the peak number of simultaneous executions.
type concurrencyTool struct {
	name string
	cur  *atomic.Int32
	peak *atomic.Int32
}

func (t *concurrencyTool) ParallelSafe() bool { return true }

func (t *concurrencyTool) Name() string { return t.name }
func (t *concurrencyTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Description: "fake", Parameters: map[string]any{"type": "object"}}
}
func (t *concurrencyTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	c := t.cur.Add(1)
	for {
		p := t.peak.Load()
		if c <= p || t.peak.CompareAndSwap(p, c) {
			break
		}
	}
	time.Sleep(100 * time.Millisecond)
	t.cur.Add(-1)
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: `{"ok":true}`}}}
}

func TestToolCallsAreBounded(t *testing.T) {
	const n = 8
	var cur, peak atomic.Int32
	names := make([]string, n)
	tools := make([]Tool, 0, n)
	for i := 0; i < n; i++ {
		names[i] = fmt.Sprintf("tool-%d", i)
		tools = append(tools, &concurrencyTool{name: names[i], cur: &cur, peak: &peak})
	}
	provider := &fakeProvider{responses: []AssistantMessage{
		{Message: Message{Role: RoleAssistant, ToolCalls: toolCallMessages(names)}, StopReason: StopReasonToolUse},
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	runner := NewRunner(provider, tools)

	start := time.Now()
	_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if p := peak.Load(); p > maxParallelToolCalls {
		t.Errorf("peak concurrency %d exceeds bound %d", p, maxParallelToolCalls)
	}
	if p := peak.Load(); p < 2 {
		t.Errorf("expected actual parallelism, peak was %d", p)
	}
	// 8 x 100ms sequential would be ~800ms; bounded(4) should be ~200ms.
	// Generous ceiling: scheduling jitter on CI must not flake this.
	if elapsed > 700*time.Millisecond {
		t.Errorf("expected parallel speedup, took %v", elapsed)
	}
}

// delayTool sleeps before returning, so results complete out of order.
type delayTool struct {
	name  string
	delay time.Duration
}

func (t *delayTool) ParallelSafe() bool { return true }

func (t *delayTool) Name() string { return t.name }
func (t *delayTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Description: "fake", Parameters: map[string]any{"type": "object"}}
}
func (t *delayTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	time.Sleep(t.delay)
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: t.name}}}
}

func TestToolCallResultsKeepCallOrder(t *testing.T) {
	names := []string{"tool-0", "tool-1", "tool-2"}
	tools := []Tool{
		&delayTool{name: "tool-0", delay: 150 * time.Millisecond},
		&delayTool{name: "tool-1", delay: 50 * time.Millisecond},
		&delayTool{name: "tool-2", delay: 0},
	}
	provider := &fakeProvider{responses: []AssistantMessage{
		{Message: Message{Role: RoleAssistant, ToolCalls: toolCallMessages(names)}, StopReason: StopReasonToolUse},
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	runner := NewRunner(provider, tools)

	_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected two provider calls, got %d", len(provider.requests))
	}
	var got []string
	for _, m := range provider.requests[1].Messages {
		if m.Role == RoleTool {
			got = append(got, m.ToolCallID)
		}
	}
	want := []string{"call-0", "call-1", "call-2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("tool messages out of order: got %v want %v", got, want)
	}
}

// orderedApprover records approval order and only lets execution proceed
// once every approval has been requested.
type orderedApprover struct {
	mu           sync.Mutex
	requests     []string
	allRequested chan struct{}
	want         int
}

func (a *orderedApprover) Approve(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	a.mu.Lock()
	a.requests = append(a.requests, req.ToolName)
	n := len(a.requests)
	a.mu.Unlock()
	if n == a.want {
		close(a.allRequested)
	}
	return ApprovalDecision{Allow: true}, nil
}

func TestApprovalsStaySequentialBeforeExecution(t *testing.T) {
	names := []string{"tool-0", "tool-1", "tool-2"}
	approver := &orderedApprover{allRequested: make(chan struct{}), want: len(names)}
	tools := make([]Tool, 0, len(names))
	for _, name := range names {
		tools = append(tools, &approvalGateTool{name: name, release: approver.allRequested})
	}
	provider := &fakeProvider{responses: []AssistantMessage{
		{Message: Message{Role: RoleAssistant, ToolCalls: toolCallMessages(names)}, StopReason: StopReasonToolUse},
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	runner := NewRunnerWithOptions(provider, tools, RunnerOptions{Approver: approver})

	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run deadlocked: execution started before all approvals were requested")
	}
	approver.mu.Lock()
	defer approver.mu.Unlock()
	want := []string{"tool-0", "tool-1", "tool-2"}
	if fmt.Sprint(approver.requests) != fmt.Sprint(want) {
		t.Errorf("approvals out of order: got %v want %v", approver.requests, want)
	}
}

// approvalGateTool is approval-gated and blocks until release closes.
type approvalGateTool struct {
	name    string
	release <-chan struct{}
}

func (t *approvalGateTool) Name() string { return t.name }
func (t *approvalGateTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Description: "fake", Parameters: map[string]any{"type": "object"}}
}
func (t *approvalGateTool) Execute(ctx context.Context, args json.RawMessage) ToolResult {
	select {
	case <-t.release:
	case <-ctx.Done():
	}
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: `{"ok":true}`}}}
}
func (t *approvalGateTool) ApprovalRequest(args json.RawMessage) (ApprovalRequest, error) {
	return ApprovalRequest{ToolName: t.name, Summary: "run " + t.name, Arguments: args}, nil
}
