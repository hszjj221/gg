package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hszjj221/gg/internal/agent"
)

type SubmitFunc func(context.Context, string, func(string)) (SubmitResult, error)

type SubmitResult struct {
	Content string
	Usage   agent.Usage
}

type Message struct {
	Role    agent.Role
	Content string
}

type Config struct {
	CWD             string
	ModelName       string
	ShowUsage       bool
	InitialMessages []Message
	Submit          SubmitFunc
	Input           io.Reader
	Output          io.Writer
}

type Model struct {
	cwd       string
	modelName string
	showUsage bool
	submit    SubmitFunc

	messages []Message
	input    textinput.Model
	viewport viewport.Model

	width           int
	height          int
	busy            bool
	cancelRequested bool
	cancel          context.CancelFunc
	updates         chan tea.Msg
	lastUsage       agent.Usage
	hasUsage        bool
	err             error
}

type streamDeltaMsg string

type submitDoneMsg struct {
	result SubmitResult
	err    error
}

func NewModel(config Config) Model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Ask gg..."
	input.Focus()

	model := Model{
		cwd:       config.CWD,
		modelName: config.ModelName,
		showUsage: config.ShowUsage,
		submit:    config.Submit,
		messages:  append([]Message(nil), config.InitialMessages...),
		input:     input,
		viewport:  viewport.New(80, 20),
		width:     80,
		height:    24,
	}
	model.applyLayout()
	model.refreshViewport()
	return model
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applyLayout()
		m.refreshViewport()
		return m, nil
	case streamDeltaMsg:
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].Role != agent.RoleAssistant {
			m.messages = append(m.messages, Message{Role: agent.RoleAssistant})
		}
		m.messages[len(m.messages)-1].Content += string(msg)
		m.refreshViewport()
		return m, waitForUpdateCmd(m.updates)
	case submitDoneMsg:
		m.busy = false
		m.cancelRequested = false
		m.cancel = nil
		m.updates = nil
		m.lastUsage = msg.result.Usage
		m.hasUsage = true
		if msg.err != nil {
			m.err = msg.err
			if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == agent.RoleAssistant && m.messages[len(m.messages)-1].Content == "" {
				m.messages = m.messages[:len(m.messages)-1]
			}
		} else {
			m.err = nil
			if len(m.messages) == 0 || m.messages[len(m.messages)-1].Role != agent.RoleAssistant {
				m.messages = append(m.messages, Message{Role: agent.RoleAssistant})
			}
			if msg.result.Content != "" {
				m.messages[len(m.messages)-1].Content = msg.result.Content
			}
		}
		m.input.Focus()
		m.refreshViewport()
		return m, nil
	case tea.KeyMsg:
		if m.busy {
			switch msg.String() {
			case "ctrl+c", "esc":
				if m.cancel != nil {
					m.cancel()
				}
				m.cancelRequested = true
				return m, nil
			case "enter":
				return m, nil
			}
		} else {
			switch msg.String() {
			case "ctrl+c", "esc":
				return m, tea.Quit
			case "enter":
				prompt := strings.TrimSpace(m.input.Value())
				if prompt == "" {
					return m, nil
				}
				return m.startSubmit(prompt)
			}
		}
	}

	var cmd tea.Cmd
	if !m.busy {
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	m.refreshViewport()
	return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), m.input.View(), m.statusLine())
}

func (m Model) startSubmit(prompt string) (Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan tea.Msg, 32)
	m.busy = true
	m.cancelRequested = false
	m.cancel = cancel
	m.updates = updates
	m.err = nil
	m.messages = append(m.messages,
		Message{Role: agent.RoleUser, Content: prompt},
		Message{Role: agent.RoleAssistant},
	)
	m.input.SetValue("")
	m.input.Blur()
	m.refreshViewport()
	return m, tea.Batch(startSubmitCmd(m.submit, ctx, prompt, updates), waitForUpdateCmd(updates))
}

func (m *Model) applyLayout() {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}
	m.input.Width = max(1, width-2)
	viewportHeight := max(1, height-2)
	m.viewport.Width = width
	m.viewport.Height = viewportHeight
}

func (m *Model) refreshViewport() {
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

func (m Model) renderMessages() string {
	if len(m.messages) == 0 {
		return mutedStyle.Render("No messages yet.")
	}
	var b strings.Builder
	for i, message := range m.messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		label := "gg"
		style := assistantLabelStyle
		if message.Role == agent.RoleUser {
			label = "you"
			style = userLabelStyle
		}
		b.WriteString(style.Render(label))
		b.WriteString("\n")
		content := strings.TrimRight(message.Content, "\n")
		if content == "" && message.Role == agent.RoleAssistant && m.busy {
			content = "..."
		}
		b.WriteString(content)
	}
	return b.String()
}

func (m Model) statusLine() string {
	state := "ready"
	if m.busy {
		state = "running"
	}
	if m.cancelRequested {
		state = "canceling"
	}
	parts := []string{"gg", shortPath(m.cwd), m.modelName, state}
	if m.showUsage && m.hasUsage {
		parts = append(parts, fmt.Sprintf("tokens: prompt=%d completion=%d total=%d", m.lastUsage.PromptTokens, m.lastUsage.CompletionTokens, m.lastUsage.TotalTokens))
	}
	if m.err != nil {
		parts = append(parts, "error: "+m.err.Error())
	}
	return statusStyle.Width(max(1, m.width)).Render(strings.Join(parts, " | "))
}

func startSubmitCmd(submit SubmitFunc, ctx context.Context, prompt string, updates chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			defer close(updates)
			if submit == nil {
				updates <- submitDoneMsg{err: fmt.Errorf("submit function is not configured")}
				return
			}
			result, err := submit(ctx, prompt, func(delta string) {
				if delta == "" {
					return
				}
				select {
				case updates <- streamDeltaMsg(delta):
				case <-ctx.Done():
				}
			})
			updates <- submitDoneMsg{result: result, err: err}
		}()
		return nil
	}
}

func waitForUpdateCmd(updates <-chan tea.Msg) tea.Cmd {
	if updates == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-updates
		if !ok {
			return nil
		}
		return msg
	}
}

func shortPath(path string) string {
	if path == "" {
		return "."
	}
	return filepath.Clean(path)
}

var (
	userLabelStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	assistantLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	mutedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	statusStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("238"))
)
