package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type SessionItem struct {
	ID           string
	Path         string
	Name         string
	Timestamp    string
	MessageCount int
	Preview      string
}

type SessionSelectorModel struct {
	items    []SessionItem
	filtered []SessionItem
	search   textinput.Model
	cursor   int
	width    int
	height   int
	selected string
	canceled bool
}

func NewSessionSelectorModel(items []SessionItem) SessionSelectorModel {
	search := textinput.New()
	search.Prompt = "Search: "
	search.Placeholder = "name, message, id, or path"
	search.Focus()
	m := SessionSelectorModel{
		items:    append([]SessionItem(nil), items...),
		filtered: append([]SessionItem(nil), items...),
		search:   search,
		width:    80,
		height:   24,
	}
	return m
}

func (m SessionSelectorModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m SessionSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.search.Width = max(1, msg.Width-10)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.cursor+1 < len(m.filtered) {
				m.cursor++
			}
			return m, nil
		case "pgup":
			m.cursor = max(0, m.cursor-m.visibleRows())
			return m, nil
		case "pgdown":
			m.cursor = min(max(0, len(m.filtered)-1), m.cursor+m.visibleRows())
			return m, nil
		case "enter":
			if len(m.filtered) == 0 {
				return m, nil
			}
			m.selected = m.filtered[m.cursor].Path
			return m, tea.Quit
		}
	}

	before := m.search.Value()
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != before {
		m.applyFilter()
	}
	return m, cmd
}

func (m SessionSelectorModel) View() string {
	var b strings.Builder
	b.WriteString(selectorTitleStyle.Render("Resume session"))
	b.WriteString("\n")
	b.WriteString(m.search.View())
	b.WriteString("\n\n")

	if len(m.filtered) == 0 {
		b.WriteString(mutedStyle.Render("No matching sessions."))
		b.WriteString("\n")
	} else {
		start, end := m.visibleRange()
		for i := start; i < end; i++ {
			item := m.filtered[i]
			marker := "  "
			style := selectorItemStyle
			if i == m.cursor {
				marker = "> "
				style = selectorSelectedStyle
			}
			b.WriteString(style.Render(marker + truncateLine(sessionItemTitle(item), max(12, m.width-2))))
			b.WriteString("\n")
			meta := fmt.Sprintf("  %s  %d messages  %s", formatSessionTime(item.Timestamp), item.MessageCount, item.ID)
			b.WriteString(mutedStyle.Render(truncateLine(meta, max(12, m.width-2))))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("↑/↓ select  Enter resume  Esc cancel"))
	return b.String()
}

func (m SessionSelectorModel) SelectedPath() string {
	return m.selected
}

func (m SessionSelectorModel) Canceled() bool {
	return m.canceled
}

func (m *SessionSelectorModel) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	m.filtered = m.filtered[:0]
	for _, item := range m.items {
		haystack := strings.ToLower(strings.Join([]string{item.Name, item.Preview, item.ID, item.Path}, "\n"))
		if query == "" || strings.Contains(haystack, query) {
			m.filtered = append(m.filtered, item)
		}
	}
	m.cursor = 0
}

func (m SessionSelectorModel) visibleRows() int {
	return max(1, (m.height-6)/2)
}

func (m SessionSelectorModel) visibleRange() (int, int) {
	rows := m.visibleRows()
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	end := min(len(m.filtered), start+rows)
	return start, end
}

func sessionItemTitle(item SessionItem) string {
	if name := strings.TrimSpace(item.Name); name != "" {
		if preview := strings.TrimSpace(item.Preview); preview != "" && preview != name {
			return name + " — " + preview
		}
		return name
	}
	if preview := strings.TrimSpace(item.Preview); preview != "" {
		return preview
	}
	return "(untitled session)"
}

func formatSessionTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return t.Local().Format("2006-01-02 15:04")
}

func truncateLine(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", max(0, width))
	}
	limit := width - 3
	used := 0
	var b strings.Builder
	for _, r := range value {
		runeWidth := lipgloss.Width(string(r))
		if used+runeWidth > limit {
			break
		}
		b.WriteRune(r)
		used += runeWidth
	}
	return b.String() + "..."
}

func RunSessionSelector(ctx context.Context, items []SessionItem, input io.Reader, output io.Writer) (string, bool, error) {
	model := NewSessionSelectorModel(items)
	options := []tea.ProgramOption{tea.WithAltScreen()}
	if input != nil {
		options = append(options, tea.WithInput(input))
	}
	if output != nil {
		options = append(options, tea.WithOutput(output))
	}
	program := tea.NewProgram(model, options...)
	type result struct {
		model tea.Model
		err   error
	}
	done := make(chan result, 1)
	go func() {
		finalModel, err := program.Run()
		done <- result{model: finalModel, err: err}
	}()
	select {
	case <-ctx.Done():
		program.Quit()
		return "", false, ctx.Err()
	case result := <-done:
		if result.err != nil {
			return "", false, result.err
		}
		finalModel, ok := result.model.(SessionSelectorModel)
		if !ok {
			return "", false, fmt.Errorf("unexpected session selector model %T", result.model)
		}
		return finalModel.SelectedPath(), !finalModel.Canceled() && finalModel.SelectedPath() != "", nil
	}
}

var (
	selectorTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	selectorItemStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selectorSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
)
