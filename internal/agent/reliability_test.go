package agent

import (
	"context"
	"errors"
	"testing"
)

func TestMessagePersistenceFailurePreventsToolExecution(t *testing.T) {
	p := &fakeProvider{responses: []AssistantMessage{{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "write", Arguments: []byte(`{}`)}}}, StopReason: StopReasonToolUse}}}
	write := &fakeApprovalTool{name: "write"}
	r := NewRunnerWithOptions(p, []Tool{write}, RunnerOptions{OnMessage: func(Message) error { return errors.New("disk full") }})
	_, err := r.Run(context.Background(), nil, nil)
	if err == nil || write.runs != 0 {
		t.Fatalf("tool ran without durable request: err=%v runs=%d", err, write.runs)
	}
}

func TestSteeringAfterFinalResponseContinuesSameRun(t *testing.T) {
	queue := &MessageQueue{}
	p := &fakeProvider{responses: []AssistantMessage{
		{Message: Message{Role: RoleAssistant, Content: "first", ContentBlocks: []ContentBlock{{Type: ContentText, Text: "first"}}}, StopReason: StopReasonEndTurn},
		{Message: Message{Role: RoleAssistant, Content: "second"}, StopReason: StopReasonEndTurn},
	}}
	r := NewRunnerWithOptions(p, nil, RunnerOptions{DrainMessages: queue.DrainSteering})
	var delivered bool
	result, err := r.Run(context.Background(), []Message{{Role: RoleUser, Content: "task"}}, func(event Event) {
		if event.Type == EventTextDelta {
			queue.Add("new constraint", false)
			queue.Add("later", true)
		}
		if event.Type == EventUserMessage {
			delivered = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "second" || len(p.requests) != 2 || !delivered {
		t.Fatal("steering was not delivered")
	}
	last := p.requests[1].Messages[len(p.requests[1].Messages)-1]
	if last.Role != RoleUser || last.Content != "new constraint" || queue.PopNext() != "later" {
		t.Fatal("steering and follow-up timing mixed")
	}
}

// Cancellation during the sequential approval phase stops the rest of the
// batch: prepare is ordered, so every call after the cancellation point is
// decided without executing.
func TestCancellationDuringApprovalStopsBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tools := []*fakeApprovalTool{{name: "a"}, {name: "b"}, {name: "c"}}
	approver := &cancelOnFirstApprove{cancel: cancel}
	var generic []Tool
	for _, tl := range tools {
		generic = append(generic, tl)
	}
	p := &fakeProvider{responses: []AssistantMessage{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "1", Name: "a", Arguments: []byte(`{}`)},
			{ID: "2", Name: "b", Arguments: []byte(`{}`)},
			{ID: "3", Name: "c", Arguments: []byte(`{}`)},
		}}, StopReason: StopReasonToolUse},
		{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
	}}
	runner := NewRunnerWithOptions(p, generic, RunnerOptions{Approver: approver})
	_, _ = runner.Run(ctx, []Message{{Role: RoleUser, Content: "work"}}, nil)
	for _, tl := range tools {
		if tl.runs != 0 {
			t.Errorf("%s executed %d times after mid-batch cancellation", tl.name, tl.runs)
		}
	}
}

type cancelOnFirstApprove struct {
	cancel context.CancelFunc
	once   bool
}

func (a *cancelOnFirstApprove) Approve(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	if !a.once {
		a.once = true
		a.cancel()
	}
	return ApprovalDecision{Allow: true}, nil
}

func TestCancellationBeforeBatchPreventsExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &fakeProvider{responses: []AssistantMessage{{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "write", Arguments: []byte(`{}`)}}}, StopReason: StopReasonToolUse}}}
	write := &fakeApprovalTool{name: "write"}
	runner := NewRunner(p, []Tool{write})
	_, _ = runner.Run(ctx, []Message{{Role: RoleUser, Content: "work"}}, nil)
	if write.runs != 0 {
		t.Fatalf("write executed %d times with cancelled context", write.runs)
	}
}

// startPreparedCall is the last checkpoint before execution: a call that
// queued behind the semaphore must not start once ctx is done.
func TestStartPreparedCallSkipsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := &fakeApprovalTool{name: "write"}
	runner := NewRunner(nil, []Tool{tool})
	result := runner.startPreparedCall(ctx, tool, ToolCall{ID: "1", Name: "write"}, "write", nil)
	if !result.IsError {
		t.Error("expected error result for cancelled start")
	}
	if tool.runs != 0 {
		t.Errorf("tool executed despite cancelled context")
	}
}
