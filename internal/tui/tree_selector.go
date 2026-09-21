package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type treeSelector struct {
	action   SessionAction
	items    []TreeItem
	filtered []TreeItem
	search   textinput.Model
	cursor   int
}

func newTreeSelector(action SessionAction, items []TreeItem) *treeSelector {
	search := textinput.New()
	search.Prompt = "Search: "
	search.Placeholder = "message text"
	search.Focus()
	selector := &treeSelector{
		action:   action,
		items:    append([]TreeItem(nil), items...),
		filtered: append([]TreeItem(nil), items...),
		search:   search,
	}
	for i := len(selector.filtered) - 1; i >= 0; i-- {
		if selector.filtered[i].Active {
			selector.cursor = i
			break
		}
	}
	return selector
}

func (s *treeSelector) Update(msg tea.KeyMsg) (selected string, canceled bool) {
	switch msg.String() {
	case "ctrl+c", "esc":
		return "", true
	case "up", "ctrl+p":
		if s.cursor > 0 {
			s.cursor--
		}
		return "", false
	case "down", "ctrl+n":
		if s.cursor+1 < len(s.filtered) {
			s.cursor++
		}
		return "", false
	case "pgup":
		s.cursor = max(0, s.cursor-10)
		return "", false
	case "pgdown":
		s.cursor = min(max(0, len(s.filtered)-1), s.cursor+10)
		return "", false
	case "enter":
		if len(s.filtered) > 0 {
			return s.filtered[s.cursor].ID, false
		}
		return "", false
	}
	before := s.search.Value()
	s.search, _ = s.search.Update(msg)
	if s.search.Value() != before {
		s.applyFilter()
	}
	return "", false
}

func (s *treeSelector) View(width, height int) string {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 23
	}
	s.search.Width = max(1, width-10)
	var b strings.Builder
	b.WriteString(selectorTitleStyle.Render(s.title()))
	b.WriteString("\n")
	b.WriteString(s.search.View())
	b.WriteString("\n\n")
	if len(s.filtered) == 0 {
		b.WriteString(mutedStyle.Render("No matching conversation nodes."))
		b.WriteString("\n")
	} else {
		start, end := s.visibleRange(height)
		for i := start; i < end; i++ {
			item := s.filtered[i]
			cursor := "  "
			style := selectorItemStyle
			if i == s.cursor {
				cursor = "> "
				style = selectorSelectedStyle
			}
			active := "○"
			if item.Active {
				active = "●"
			}
			role := "gg"
			switch item.Role {
			case "user":
				role = "you"
			case "tool":
				role = "tool"
			}
			indent := strings.Repeat("  ", item.Depth)
			text := strings.Join(strings.Fields(item.Text), " ")
			if text == "" {
				text = "(no text)"
			}
			line := fmt.Sprintf("%s%s%s─ %s: %s", cursor, indent, active, role, text)
			b.WriteString(style.Render(truncateLine(line, max(12, width))))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(s.help()))
	return b.String()
}

func (s *treeSelector) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(s.search.Value()))
	s.filtered = s.filtered[:0]
	for _, item := range s.items {
		if query == "" || strings.Contains(strings.ToLower(item.Text), query) {
			s.filtered = append(s.filtered, item)
		}
	}
	s.cursor = 0
	for i := len(s.filtered) - 1; i >= 0; i-- {
		if s.filtered[i].Active {
			s.cursor = i
			break
		}
	}
}

func (s *treeSelector) visibleRange(height int) (int, int) {
	rows := max(1, height-5)
	start := 0
	if s.cursor >= rows {
		start = s.cursor - rows + 1
	}
	return start, min(len(s.filtered), start+rows)
}

func (s *treeSelector) title() string {
	if s.action == SessionActionFork {
		return "Fork from message"
	}
	return "Conversation tree"
}

func (s *treeSelector) help() string {
	if s.action == SessionActionFork {
		return "↑/↓ select  Enter fork and edit  Esc cancel"
	}
	return "● active branch  ↑/↓ select  Enter switch/edit  Esc cancel"
}
