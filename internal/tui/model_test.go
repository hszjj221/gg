package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hszjj221/gg/internal/agent"
)

func TestInitialViewContainsInputAndStatus(t *testing.T) {
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit:    successSubmit("hello"),
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})

	view := model.View()
	for _, want := range []string{"gg", "/tmp/project", "openai:gpt-test"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if !model.input.Focused() {
		t.Fatalf("input should be focused")
	}
}

func TestEnterSubmitsPromptAndRecordsUsage(t *testing.T) {
	var prompts []string
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		ShowUsage: true,
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			prompts = append(prompts, prompt)
			return SubmitResult{
				Content:   "assistant reply",
				ModelName: "local:qwen2.5-coder",
				Usage:     agent.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
			}, nil
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("say hi")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !model.busy {
		t.Fatalf("model should be busy after submit")
	}
	if got := len(model.messages); got != 2 {
		t.Fatalf("expected user and pending assistant messages, got %d", got)
	}
	if model.messages[0].Content != "say hi" {
		t.Fatalf("unexpected user message: %+v", model.messages[0])
	}

	model = drainCommands(t, model, cmd)

	if len(prompts) != 1 || prompts[0] != "say hi" {
		t.Fatalf("submit prompts = %+v", prompts)
	}
	if model.busy {
		t.Fatalf("model should not be busy after completion")
	}
	if got := model.messages[len(model.messages)-1].Content; got != "assistant reply" {
		t.Fatalf("unexpected assistant message: %q", got)
	}
	if got := model.lastUsage.TotalTokens; got != 5 {
		t.Fatalf("usage not recorded: %+v", model.lastUsage)
	}
	view := model.View()
	if !strings.Contains(view, "tokens: prompt=3") || !strings.Contains(view, "completion=2") || !strings.Contains(view, "total=5") {
		t.Fatalf("usage not rendered:\n%s", model.View())
	}
	if !strings.Contains(view, "local:qwen2.5-coder") {
		t.Fatalf("model name not updated:\n%s", model.View())
	}
}

func TestStreamingDeltaUpdatesPendingAssistantMessage(t *testing.T) {
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			onEvent(agent.Event{Type: agent.EventTextDelta, Text: "he"})
			onEvent(agent.Event{Type: agent.EventTextDelta, Text: "llo"})
			return SubmitResult{Content: "hello"}, nil
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("say hi")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = drainCommands(t, model, cmd)

	if got := model.messages[len(model.messages)-1].Content; got != "hello" {
		t.Fatalf("streamed assistant content = %q", got)
	}
}

func TestToolEventsRenderInlineLogAndThenAssistant(t *testing.T) {
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			onEvent(agent.Event{Type: agent.EventToolCallStart, ToolCallID: "call-1", ToolName: "read", Summary: "read README.md"})
			onEvent(agent.Event{Type: agent.EventToolCallFinish, ToolCallID: "call-1", ToolName: "read", Summary: "read README.md", Details: "line 1: gg"})
			onEvent(agent.Event{Type: agent.EventTextDelta, Text: "answer"})
			return SubmitResult{Content: "answer"}, nil
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("inspect file")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = drainCommands(t, model, cmd)

	if got := len(model.messages); got != 3 {
		t.Fatalf("expected user, tool, assistant messages, got %d: %+v", got, model.messages)
	}
	if model.messages[1].Role != agent.RoleTool || model.messages[1].ToolStatus != "done" {
		t.Fatalf("tool log not updated: %+v", model.messages[1])
	}
	if got := model.messages[2].Content; got != "answer" {
		t.Fatalf("assistant message after tool log = %q", got)
	}
	view := model.View()
	for _, want := range []string{"tool read done", "read README.md", "line 1: gg", "answer"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestToolFinishUpdatesSameLog(t *testing.T) {
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			onEvent(agent.Event{Type: agent.EventToolCallStart, ToolCallID: "call-1", ToolName: "grep", Summary: "grep TODO"})
			onEvent(agent.Event{Type: agent.EventToolCallFinish, ToolCallID: "call-1", ToolName: "grep", Summary: "grep TODO", Details: "found 2 matches"})
			return SubmitResult{Content: "done"}, nil
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("search")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = drainCommands(t, model, cmd)

	toolLogs := 0
	for _, message := range model.messages {
		if message.Role == agent.RoleTool {
			toolLogs++
			if message.ToolStatus != "done" || !strings.Contains(message.Content, "found 2 matches") {
				t.Fatalf("unexpected tool log: %+v", message)
			}
		}
	}
	if toolLogs != 1 {
		t.Fatalf("expected one tool log, got %d: %+v", toolLogs, model.messages)
	}
}

func TestToolErrorAndDeniedStatuses(t *testing.T) {
	tests := []struct {
		name    string
		details string
		want    string
	}{
		{name: "error", details: "exit status 1", want: "error"},
		{name: "permission denied", details: "open file.txt: permission denied", want: "error"},
		{name: "denied", details: `tool call "bash" denied by user`, want: "denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := NewModel(Config{
				CWD:       "/tmp/project",
				ModelName: "openai:gpt-test",
				Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
					onEvent(agent.Event{Type: agent.EventToolCallStart, ToolCallID: "call-1", ToolName: "bash", Summary: "bash: false"})
					onEvent(agent.Event{Type: agent.EventToolCallFinish, ToolCallID: "call-1", ToolName: "bash", Summary: "bash: false", Details: tt.details, IsError: true})
					return SubmitResult{Content: "handled"}, nil
				},
			})
			model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
			model.input.SetValue("run")

			model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
			model = drainCommands(t, model, cmd)

			if got := model.messages[1].ToolStatus; got != tt.want {
				t.Fatalf("tool status = %q, want %q", got, tt.want)
			}
			if !strings.Contains(model.View(), "tool bash "+tt.want) {
				t.Fatalf("status not rendered:\n%s", model.View())
			}
		})
	}
}

func TestToolLogTruncatesLongDetails(t *testing.T) {
	longDetails := strings.Repeat("x", toolLogPreviewLimit+100)
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			onEvent(agent.Event{Type: agent.EventToolCallStart, ToolCallID: "call-1", ToolName: "subagent", Summary: "subagent investigate", Details: longDetails})
			return SubmitResult{}, nil
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("delegate")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = drainCommands(t, model, cmd)

	if got := len([]rune(model.messages[1].Content)); got != toolLogPreviewLimit+3 {
		t.Fatalf("tool log length = %d, want %d", got, toolLogPreviewLimit+3)
	}
	if !strings.HasSuffix(model.messages[1].Content, "...") {
		t.Fatalf("tool log was not truncated: %q", model.messages[1].Content)
	}
}

func TestSubmitErrorLeavesModelIdleAndShowsError(t *testing.T) {
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			return SubmitResult{}, errors.New("provider failed")
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("say hi")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = drainCommands(t, model, cmd)

	if model.busy {
		t.Fatalf("model should be idle after error")
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "provider failed") {
		t.Fatalf("error not recorded: %v", model.err)
	}
	if !strings.Contains(model.View(), "provider failed") {
		t.Fatalf("error not rendered:\n%s", model.View())
	}
}

func TestBusyEnterDoesNotSubmitAgain(t *testing.T) {
	calls := 0
	started := make(chan struct{}, 1)
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			calls++
			started <- struct{}{}
			<-ctx.Done()
			return SubmitResult{}, ctx.Err()
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("first")
	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	startOnly(t, cmd)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("submit did not start")
	}
	model.input.SetValue("second")

	model, secondCmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if secondCmd != nil {
		t.Fatalf("busy enter should not return a submit command")
	}
	if got := len(model.messages); got != 2 {
		t.Fatalf("busy enter should not append messages, got %d", got)
	}
	if calls != 1 {
		t.Fatalf("submit calls = %d", calls)
	}
	if model.cancel != nil {
		model.cancel()
	}
}

func TestResizeUpdatesLayout(t *testing.T) {
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit:    successSubmit("hello"),
	})

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 30})

	if model.width != 100 || model.height != 30 {
		t.Fatalf("size not recorded: %dx%d", model.width, model.height)
	}
	if model.viewport.Width <= 0 || model.viewport.Height <= 0 {
		t.Fatalf("viewport size not updated: %+v", model.viewport)
	}
}

func TestApprovalRequestCanBeApproved(t *testing.T) {
	model := NewModel(Config{
		CWD:            "/tmp/project",
		ModelName:      "openai:gpt-test",
		EnableApproval: true,
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			decision, err := approver.Approve(ctx, agent.ApprovalRequest{
				ToolName: "bash",
				Summary:  "bash: go test ./...",
				Details:  "command: go test ./...",
			})
			if err != nil {
				return SubmitResult{}, err
			}
			if !decision.Allow {
				return SubmitResult{Content: "denied"}, nil
			}
			return SubmitResult{Content: "approved"}, nil
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("run tests")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	msg := nextMsg(t, cmd)
	model, cmd = updateModel(t, model, msg)

	if model.approval == nil || !strings.Contains(model.View(), "Approve tool call") || !strings.Contains(model.View(), "go test ./...") {
		t.Fatalf("approval request not rendered:\n%s", model.View())
	}

	model, cmd = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = drainCommands(t, model, cmd)

	if model.busy || model.approval != nil {
		t.Fatalf("model should be idle after approving")
	}
	if got := model.messages[len(model.messages)-1].Content; got != "approved" {
		t.Fatalf("unexpected approved content: %q", got)
	}
}

func TestApprovalRequestCanBeDenied(t *testing.T) {
	model := NewModel(Config{
		CWD:            "/tmp/project",
		ModelName:      "openai:gpt-test",
		EnableApproval: true,
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
			decision, err := approver.Approve(ctx, agent.ApprovalRequest{ToolName: "write", Summary: "write overwrite file.txt"})
			if err != nil {
				return SubmitResult{}, err
			}
			if decision.Allow {
				return SubmitResult{Content: "approved"}, nil
			}
			return SubmitResult{Content: "denied"}, nil
		},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 80, Height: 20})
	model.input.SetValue("write file")

	model, cmd := updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	msg := nextMsg(t, cmd)
	model, cmd = updateModel(t, model, msg)
	model, cmd = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = drainCommands(t, model, cmd)

	if model.busy || model.approval != nil {
		t.Fatalf("model should be idle after denying")
	}
	if got := model.messages[len(model.messages)-1].Content; got != "denied" {
		t.Fatalf("unexpected denied content: %q", got)
	}
}

func successSubmit(content string) SubmitFunc {
	return func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (SubmitResult, error) {
		return SubmitResult{Content: content}, nil
	}
}

func updateModel(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}
	return model, cmd
}

func drainCommands(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()
	for cmd != nil {
		msg := nextMsg(t, cmd)
		if msg == nil {
			return model
		}
		var next tea.Cmd
		model, next = updateModel(t, model, msg)
		cmd = next
		if _, ok := msg.(submitDoneMsg); ok {
			return model
		}
	}
	return model
}

func startOnly(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		if len(batch) == 0 {
			t.Fatalf("expected batch commands")
		}
		if batch[0] != nil {
			_ = batch[0]()
		}
		return
	}
	t.Fatalf("expected batch command, got %T", msg)
}

func nextMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	ch := make(chan tea.Msg, 1)
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				if sub == nil {
					continue
				}
				if subMsg := sub(); subMsg != nil {
					ch <- subMsg
					return
				}
			}
			ch <- nil
			return
		}
		ch <- msg
	}()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for command")
		return nil
	}
}
