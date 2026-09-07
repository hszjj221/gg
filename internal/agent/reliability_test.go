package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type cancelTool struct{ cancel context.CancelFunc }

func (t cancelTool) Name() string               { return "cancel" }
func (t cancelTool) Definition() ToolDefinition { return ToolDefinition{Name: "cancel"} }
func (t cancelTool) Execute(context.Context, json.RawMessage) ToolResult {
	t.cancel()
	return ToolResult{}
}

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
func TestCancellationStopsRemainingToolBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakeProvider{responses: []AssistantMessage{{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "cancel", Arguments: []byte(`{}`)}, {ID: "2", Name: "write", Arguments: []byte(`{}`)}}}, StopReason: StopReasonToolUse}}}
	write := &fakeApprovalTool{name: "write"}
	runner := NewRunner(p, []Tool{cancelTool{cancel}, write})
	_, _ = runner.Run(ctx, []Message{{Role: RoleUser, Content: "work"}}, nil)
	if write.runs != 0 {
		t.Fatalf("write executed %d times after cancellation", write.runs)
	}
}
