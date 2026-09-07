package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hszjj221/gg/internal/agent"
)

func TestBusyInputQueuesSteeringAndFollowUp(t *testing.T) {
	m := NewModel(Config{})
	m.busy = true
	m.input.SetValue("adjust current task")
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.input.SetValue("next task")
	m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	steering := m.queue.DrainSteering()
	if len(steering) != 1 || steering[0].Content != "adjust current task" || m.queue.PopNext() != "next task" {
		t.Fatal("incorrect queue routing")
	}
}

func TestCancellationRestoresQueuedInput(t *testing.T) {
	m := NewModel(Config{})
	m.busy = true
	m.queue.Add("steer", false)
	m.queue.Add("follow", true)
	m.input.SetValue("draft")
	m, cmd := updateModel(t, m, submitDoneMsg{err: context.Canceled})
	if cmd != nil || m.busy || m.queue.Len() != 0 {
		t.Fatal("canceled run should not start queued work")
	}
	for _, want := range []string{"steer", "follow", "draft"} {
		if !strings.Contains(m.input.Value(), want) {
			t.Fatalf("lost queued input: %q", m.input.Value())
		}
	}
}

func TestSuccessfulRunStartsFollowUpAndKeepsDraft(t *testing.T) {
	m := NewModel(Config{Submit: successSubmit("done")})
	m.busy = true
	m.queue.Add("next", true)
	m.input.SetValue("draft")
	m, cmd := updateModel(t, m, submitDoneMsg{result: SubmitResult{Content: "first"}})
	if !m.busy || cmd == nil || m.input.Value() != "draft" {
		t.Fatal("follow-up did not start or draft was lost")
	}
	m = drainCommands(t, m, cmd)
	if m.busy {
		t.Fatal("follow-up did not finish")
	}
}

func TestStreamingDoesNotForceViewportToBottom(t *testing.T) {
	m := NewModel(Config{InitialMessages: []Message{{Role: agent.RoleAssistant, Content: strings.Repeat("line\n", 100)}}})
	m.viewport.GotoTop()
	m, _ = updateModel(t, m, agentEventMsg{Type: agent.EventTextDelta, Text: "new text"})
	if m.viewport.YOffset != 0 {
		t.Fatal("streaming interrupted reading older messages")
	}
}
