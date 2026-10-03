package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type panicParallelTool struct{}

func (panicParallelTool) Name() string                                        { return "panic" }
func (panicParallelTool) Definition() ToolDefinition                          { return ToolDefinition{Name: "panic"} }
func (panicParallelTool) ParallelSafe() bool                                  { return true }
func (panicParallelTool) Execute(context.Context, json.RawMessage) ToolResult { panic("worker fault") }
func TestToolWorkerPanicBecomesPairedErrorResult(t *testing.T) {
	r := NewRunner(nil, []Tool{panicParallelTool{}})
	finishes := 0
	out := r.executeToolCalls(context.Background(), toolCallMessages([]string{"panic"}), func(e Event) {
		if e.Type == EventToolCallFinish {
			finishes++
		}
	})
	if len(out) != 1 || !out[0].IsError || !strings.Contains(resultText(out[0]), "worker fault") || finishes != 1 {
		t.Fatalf("unpaired panic result: %+v finishes=%d", out, finishes)
	}
}

type serialBarrierTool struct {
	name    string
	started chan<- string
	release <-chan struct{}
}

func (t serialBarrierTool) Name() string               { return t.name }
func (t serialBarrierTool) Definition() ToolDefinition { return ToolDefinition{Name: t.name} }
func (t serialBarrierTool) Execute(ctx context.Context, _ json.RawMessage) ToolResult {
	t.started <- t.name
	select {
	case <-t.release:
	case <-ctx.Done():
	}
	return ToolResult{}
}
func TestToolsWithoutParallelOptInExecuteInCallOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan string, 2)
	release := make(chan struct{})
	r := NewRunner(nil, []Tool{serialBarrierTool{"first", started, release}, serialBarrierTool{"second", started, release}})
	done := make(chan struct{})
	go func() { r.executeToolCalls(ctx, toolCallMessages([]string{"first", "second"}), nil); close(done) }()
	select {
	case name := <-started:
		if name != "first" {
			t.Fatalf("first execution=%s", name)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-started:
		t.Fatal("second mutation overlapped first")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
