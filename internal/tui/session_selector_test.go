package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSessionSelectorFiltersByNamePreviewIDAndPath(t *testing.T) {
	items := []SessionItem{
		{ID: "aaa111", Path: "/sessions/auth.jsonl", Name: "Refactor auth", Preview: "update middleware"},
		{ID: "bbb222", Path: "/sessions/cache.jsonl", Name: "Cache work", Preview: "add redis"},
	}
	tests := []string{"auth", "redis", "bbb222", "cache.jsonl"}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			model := NewSessionSelectorModel(items)
			model.search.SetValue(query)
			model.applyFilter()
			if len(model.filtered) != 1 {
				t.Fatalf("query %q matched %+v", query, model.filtered)
			}
		})
	}
}

func TestSessionSelectorNavigatesAndSelects(t *testing.T) {
	model := NewSessionSelectorModel([]SessionItem{
		{ID: "one", Path: "/one", Preview: "first"},
		{ID: "two", Path: "/two", Name: "Second"},
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(SessionSelectorModel)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(SessionSelectorModel)
	if cmd == nil || model.SelectedPath() != "/two" || model.Canceled() {
		t.Fatalf("unexpected selection: path=%q canceled=%v", model.SelectedPath(), model.Canceled())
	}
}

func TestSessionSelectorEscapeCancels(t *testing.T) {
	model := NewSessionSelectorModel([]SessionItem{{ID: "one", Path: "/one"}})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(SessionSelectorModel)
	if cmd == nil || !model.Canceled() || model.SelectedPath() != "" {
		t.Fatalf("selector did not cancel: %+v", model)
	}
}

func TestSessionSelectorViewShowsNameAndPreview(t *testing.T) {
	model := NewSessionSelectorModel([]SessionItem{{
		ID: "abc123", Name: "Release audit", Preview: "check CI", MessageCount: 4,
	}})
	view := model.View()
	for _, want := range []string{"Resume session", "Release audit", "check CI", "4 messages", "abc123"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestTruncateLineUsesTerminalCellWidth(t *testing.T) {
	got := truncateLine("中文会话名称", 9)
	if lipgloss.Width(got) > 9 || !strings.HasSuffix(got, "...") {
		t.Fatalf("unexpected truncation %q width=%d", got, lipgloss.Width(got))
	}
}
