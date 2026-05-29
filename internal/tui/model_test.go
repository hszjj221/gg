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
		Submit: func(ctx context.Context, prompt string, onDelta func(string)) (SubmitResult, error) {
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
		Submit: func(ctx context.Context, prompt string, onDelta func(string)) (SubmitResult, error) {
			onDelta("he")
			onDelta("llo")
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

func TestSubmitErrorLeavesModelIdleAndShowsError(t *testing.T) {
	model := NewModel(Config{
		CWD:       "/tmp/project",
		ModelName: "openai:gpt-test",
		Submit: func(ctx context.Context, prompt string, onDelta func(string)) (SubmitResult, error) {
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
		Submit: func(ctx context.Context, prompt string, onDelta func(string)) (SubmitResult, error) {
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

func successSubmit(content string) SubmitFunc {
	return func(ctx context.Context, prompt string, onDelta func(string)) (SubmitResult, error) {
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
