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
	"github.com/hszjj221/gg/internal/app"
)

type SubmitFunc func(context.Context, string, func(agent.Event), agent.Approver) (app.Result, error)
type RenameSessionFunc func(string) error
type SessionActionFunc func(app.SessionAction, string) (app.SessionUpdate, error)

type SubmitResult = app.Result
type SessionAction = app.SessionAction
type TreeItem = app.TreeItem
type SessionUpdate = app.SessionUpdate

const (
	SessionActionTree  = app.SessionActionTree
	SessionActionFork  = app.SessionActionFork
	SessionActionClone = app.SessionActionClone
)

type Message struct {
	Role       agent.Role
	Content    string
	ToolCallID string
	ToolName   string
	ToolStatus string
}

type Config struct {
	Context         context.Context
	Queue           *agent.MessageQueue
	CWD             string
	ModelName       string
	SessionName     string
	ShowUsage       bool
	EnableApproval  bool
	InitialMessages []Message
	TreeItems       []TreeItem
	Submit          SubmitFunc
	RenameSession   RenameSessionFunc
	SessionAction   SessionActionFunc
	Input           io.Reader
	Output          io.Writer
}

type Model struct {
	ctx            context.Context
	queue          *agent.MessageQueue
	cwd            string
	modelName      string
	sessionName    string
	showUsage      bool
	enableApproval bool
	submit         SubmitFunc
	renameSession  RenameSessionFunc
	sessionAction  SessionActionFunc

	messages     []Message
	treeItems    []TreeItem
	treeSelector *treeSelector
	input        textinput.Model
	viewport     viewport.Model

	width           int
	height          int
	busy            bool
	cancelRequested bool
	cancel          context.CancelFunc
	updates         chan tea.Msg
	approval        *pendingApproval
	lastUsage       agent.Usage
	hasUsage        bool
	err             error
	notice          string
}

type agentEventMsg agent.Event

type submitDoneMsg struct {
	result SubmitResult
	err    error
}

type approvalRequestMsg struct {
	request  agent.ApprovalRequest
	response chan approvalResponse
}

type approvalResponse struct {
	decision agent.ApprovalDecision
	err      error
}

type pendingApproval struct {
	request  agent.ApprovalRequest
	response chan approvalResponse
}

func NewModel(config Config) Model {
	if config.Context == nil {
		config.Context = context.Background()
	}
	if config.Queue == nil {
		config.Queue = &agent.MessageQueue{}
	}
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Ask gg..."
	input.Focus()

	model := Model{
		ctx:            config.Context,
		queue:          config.Queue,
		cwd:            config.CWD,
		modelName:      config.ModelName,
		sessionName:    config.SessionName,
		showUsage:      config.ShowUsage,
		enableApproval: config.EnableApproval,
		submit:         config.Submit,
		renameSession:  config.RenameSession,
		sessionAction:  config.SessionAction,
		messages:       append([]Message(nil), config.InitialMessages...),
		treeItems:      append([]TreeItem(nil), config.TreeItems...),
		input:          input,
		viewport:       viewport.New(80, 20),
		width:          80,
		height:         24,
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
	case agentEventMsg:
		m.handleAgentEvent(agent.Event(msg))
		m.refreshViewport()
		return m, waitForUpdateCmd(m.updates)
	case submitDoneMsg:
		if m.cancel != nil {
			m.cancel()
		}
		m.busy = false
		m.cancelRequested = false
		m.cancel = nil
		m.updates = nil
		m.approval = nil
		m.lastUsage = msg.result.Usage
		m.hasUsage = true
		if msg.result.TreeItems != nil {
			m.treeItems = append([]TreeItem(nil), msg.result.TreeItems...)
		}
		if msg.err != nil {
			m.err = msg.err
			if len(m.messages) > 0 && m.messages[len(m.messages)-1].Role == agent.RoleAssistant && m.messages[len(m.messages)-1].Content == "" {
				m.messages = m.messages[:len(m.messages)-1]
			}
		} else {
			m.err = nil
			if msg.result.ModelName != "" {
				m.modelName = msg.result.ModelName
			}
			if msg.result.Content != "" {
				if len(m.messages) == 0 || m.messages[len(m.messages)-1].Role != agent.RoleAssistant {
					m.messages = append(m.messages, Message{Role: agent.RoleAssistant})
				}
				m.messages[len(m.messages)-1].Content = msg.result.Content
			} else {
				m.removeEmptyPendingAssistant()
			}
		}
		m.input.Focus()
		m.refreshViewport()
		if msg.err != nil {
			var pending []string
			for text := m.queue.PopNext(); text != ""; text = m.queue.PopNext() {
				pending = append(pending, text)
			}
			if m.input.Value() != "" {
				pending = append(pending, m.input.Value())
			}
			m.input.SetValue(strings.Join(pending, "\n"))
		} else if next := m.queue.PopNext(); next != "" {
			draft := m.input.Value()
			updated, cmd := m.startSubmit(next)
			updated.input.SetValue(draft)
			return updated, cmd
		}
		return m, nil
	case approvalRequestMsg:
		m.approval = &pendingApproval{request: msg.request, response: msg.response}
		m.refreshViewport()
		return m, nil
	case tea.KeyMsg:
		if m.treeSelector != nil {
			return m.updateTreeSelector(msg)
		}
		if msg.String() == "pgup" || msg.String() == "pgdown" {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		if m.approval != nil {
			switch msg.String() {
			case "y", "Y":
				return m.resolveApproval(agent.ApprovalDecision{Allow: true}, nil)
			case "n", "N", "esc":
				return m.resolveApproval(agent.ApprovalDecision{Allow: false}, nil)
			case "ctrl+c":
				if m.cancel != nil {
					m.cancel()
				}
				m.cancelRequested = true
				return m.resolveApproval(agent.ApprovalDecision{Allow: false}, context.Canceled)
			case "enter":
				return m, nil
			}
		}
		if m.busy {
			switch msg.String() {
			case "ctrl+c", "esc":
				if m.cancel != nil {
					m.cancel()
				}
				m.cancelRequested = true
				return m, nil
			case "enter", "alt+enter":
				prompt := strings.TrimSpace(m.input.Value())
				if isSessionCommand(prompt) {
					m.notice = "wait until the current response finishes to manage the session"
					return m, nil
				}
				if prompt != "" && !m.cancelRequested {
					m.queue.Add(prompt, msg.String() == "alt+enter")
					m.input.SetValue("")
				}
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
				if m.handleSessionCommand(prompt) {
					m.input.SetValue("")
					return m, nil
				}
				return m.startSubmit(prompt)
			}
		}
	}

	var cmd tea.Cmd
	if m.approval == nil {
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	if m.treeSelector != nil {
		return lipgloss.JoinVertical(lipgloss.Left, m.treeSelector.View(m.width, m.height-1), m.statusLine())
	}
	parts := []string{m.viewport.View()}
	if m.approval != nil {
		parts = append(parts, m.approvalPanel())
	}
	parts = append(parts, m.input.View(), m.statusLine())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) startSubmit(prompt string) (Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	updates := make(chan tea.Msg, 32)
	m.busy = true
	m.cancelRequested = false
	m.cancel = cancel
	m.updates = updates
	m.err = nil
	m.notice = ""
	m.messages = append(m.messages,
		Message{Role: agent.RoleUser, Content: prompt},
		Message{Role: agent.RoleAssistant},
	)
	m.input.SetValue("")
	m.input.Focus()
	m.refreshViewport()
	return m, tea.Batch(startSubmitCmd(m.submit, m.ctx, ctx, prompt, updates, m.enableApproval), waitForUpdateCmd(updates))
}

func (m *Model) handleSessionCommand(prompt string) bool {
	switch prompt {
	case "/tree":
		return m.openTreeSelector(SessionActionTree)
	case "/fork":
		return m.openTreeSelector(SessionActionFork)
	case "/clone":
		if m.sessionAction == nil {
			m.err = fmt.Errorf("session persistence is disabled")
			m.notice = ""
			return true
		}
		update, err := m.sessionAction(SessionActionClone, "")
		if err != nil {
			m.err = err
			m.notice = ""
			return true
		}
		m.applySessionUpdate(update)
		return true
	}
	if prompt == "/name" {
		if m.sessionName == "" {
			m.notice = "session is unnamed; use /name <name>"
		} else {
			m.notice = "session: " + m.sessionName
		}
		return true
	}
	if !strings.HasPrefix(prompt, "/name ") {
		return false
	}
	name := strings.TrimSpace(strings.TrimPrefix(prompt, "/name "))
	if name == "--clear" {
		name = ""
	}
	if m.renameSession == nil {
		m.err = fmt.Errorf("session persistence is disabled")
		m.notice = ""
		return true
	}
	if err := m.renameSession(name); err != nil {
		m.err = err
		m.notice = ""
		return true
	}
	m.err = nil
	m.sessionName = name
	if name == "" {
		m.notice = "session name cleared"
	} else {
		m.notice = "session named: " + name
	}
	return true
}

func isSessionCommand(prompt string) bool {
	return prompt == "/name" || strings.HasPrefix(prompt, "/name ") || prompt == "/tree" || prompt == "/fork" || prompt == "/clone"
}

func (m *Model) openTreeSelector(action SessionAction) bool {
	if m.sessionAction == nil {
		m.err = fmt.Errorf("session persistence is disabled")
		m.notice = ""
		return true
	}
	items := append([]TreeItem(nil), m.treeItems...)
	if action == SessionActionFork {
		filtered := items[:0]
		for _, item := range items {
			if item.Role == agent.RoleUser {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if len(items) == 0 {
		m.err = nil
		m.notice = "no conversation nodes available"
		return true
	}
	m.treeSelector = newTreeSelector(action, items)
	m.input.Blur()
	m.err = nil
	m.notice = ""
	return true
}

func (m *Model) updateTreeSelector(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	selected, canceled := m.treeSelector.Update(msg)
	if canceled {
		m.treeSelector = nil
		m.input.Focus()
		return *m, textinput.Blink
	}
	if selected == "" {
		return *m, nil
	}
	action := m.treeSelector.action
	update, err := m.sessionAction(action, selected)
	if err != nil {
		m.err = err
		m.notice = ""
		return *m, nil
	}
	m.treeSelector = nil
	m.applySessionUpdate(update)
	m.input.Focus()
	return *m, textinput.Blink
}

func (m *Model) applySessionUpdate(update SessionUpdate) {
	m.messages = displayMessages(update.Messages)
	m.treeItems = append([]TreeItem(nil), update.TreeItems...)
	m.sessionName = update.SessionName
	m.input.SetValue(update.Draft)
	m.err = nil
	m.notice = update.Notice
	m.hasUsage = false
	m.refreshViewport()
}

func displayMessages(messages []agent.Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Content == "" {
			continue
		}
		switch message.Role {
		case agent.RoleUser, agent.RoleAssistant:
			out = append(out, Message{Role: message.Role, Content: message.Content})
		}
	}
	return out
}

func (m *Model) handleAgentEvent(event agent.Event) {
	switch event.Type {
	case agent.EventUserMessage:
		m.removeEmptyPendingAssistant()
		m.messages = append(m.messages, Message{Role: agent.RoleUser, Content: event.Text})
	case agent.EventTextDelta:
		m.appendAssistantDelta(event.Text)
	case agent.EventToolCallStart:
		m.startToolLog(event)
	case agent.EventToolCallFinish:
		m.finishToolLog(event)
	}
}

func (m *Model) appendAssistantDelta(text string) {
	if text == "" {
		return
	}
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].Role != agent.RoleAssistant {
		m.messages = append(m.messages, Message{Role: agent.RoleAssistant})
	}
	m.messages[len(m.messages)-1].Content += text
}

func (m *Model) startToolLog(event agent.Event) {
	m.removeEmptyPendingAssistant()
	m.messages = append(m.messages, Message{
		Role:       agent.RoleTool,
		Content:    toolEventContent(event),
		ToolCallID: event.ToolCallID,
		ToolName:   event.ToolName,
		ToolStatus: "running",
	})
}

func (m *Model) finishToolLog(event agent.Event) {
	status := "done"
	if event.IsError {
		status = "error"
		if strings.Contains(strings.ToLower(event.Details), "denied by user") {
			status = "denied"
		}
	}
	content := toolEventContent(event)
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == agent.RoleTool && m.messages[i].ToolCallID == event.ToolCallID {
			m.messages[i].ToolName = event.ToolName
			m.messages[i].ToolStatus = status
			m.messages[i].Content = content
			return
		}
	}
	m.removeEmptyPendingAssistant()
	m.messages = append(m.messages, Message{
		Role:       agent.RoleTool,
		Content:    content,
		ToolCallID: event.ToolCallID,
		ToolName:   event.ToolName,
		ToolStatus: status,
	})
}

func (m *Model) removeEmptyPendingAssistant() {
	if len(m.messages) == 0 {
		return
	}
	last := m.messages[len(m.messages)-1]
	if last.Role == agent.RoleAssistant && last.Content == "" {
		m.messages = m.messages[:len(m.messages)-1]
	}
}

func (m Model) resolveApproval(decision agent.ApprovalDecision, err error) (Model, tea.Cmd) {
	pending := m.approval
	m.approval = nil
	if pending != nil {
		pending.response <- approvalResponse{decision: decision, err: err}
	}
	return m, waitForUpdateCmd(m.updates)
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
	follow := m.viewport.AtBottom()
	m.viewport.SetContent(m.renderMessages())
	if follow {
		m.viewport.GotoBottom()
	}
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
		switch message.Role {
		case agent.RoleUser:
			label = "you"
			style = userLabelStyle
		case agent.RoleTool:
			label = toolLabel(message)
			style = toolLabelStyle
		}
		b.WriteString(style.Render(label))
		b.WriteString("\n")
		content := strings.TrimRight(message.Content, "\n")
		if content == "" && message.Role == agent.RoleAssistant && m.busy {
			content = "..."
		}
		if message.Role == agent.RoleTool {
			content = toolBodyStyle.Render(content)
		}
		b.WriteString(content)
	}
	return b.String()
}

func toolLabel(message Message) string {
	parts := []string{"tool"}
	if message.ToolName != "" {
		parts = append(parts, message.ToolName)
	}
	if message.ToolStatus != "" {
		parts = append(parts, message.ToolStatus)
	}
	return strings.Join(parts, " ")
}

func toolEventContent(event agent.Event) string {
	var parts []string
	summary := strings.TrimSpace(event.Summary)
	details := strings.TrimSpace(event.Details)
	if summary != "" {
		parts = append(parts, summary)
	}
	if details != "" && details != summary {
		parts = append(parts, details)
	}
	if len(parts) == 0 {
		parts = append(parts, event.ToolName)
	}
	return truncateText(strings.Join(parts, "\n"), toolLogPreviewLimit)
}

func truncateText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func (m Model) statusLine() string {
	state := "ready"
	if m.busy {
		state = "running"
	}
	if m.cancelRequested {
		state = "canceling"
	} else if m.approval != nil {
		state = "approval"
	}
	parts := []string{"gg", shortPath(m.cwd)}
	if m.sessionName != "" {
		parts = append(parts, "session: "+m.sessionName)
	}
	parts = append(parts, m.modelName, state)
	if count := m.queue.Len(); count > 0 {
		parts = append(parts, fmt.Sprintf("queued: %d", count))
	}
	if m.busy && m.approval == nil {
		parts = append(parts, "Enter: steer | Alt+Enter: follow-up")
	}
	if m.showUsage && m.hasUsage {
		parts = append(parts, fmt.Sprintf("tokens: prompt=%d completion=%d total=%d", m.lastUsage.PromptTokens, m.lastUsage.CompletionTokens, m.lastUsage.TotalTokens))
	}
	if m.err != nil {
		parts = append(parts, "error: "+m.err.Error())
	} else if m.notice != "" {
		parts = append(parts, m.notice)
	}
	return statusStyle.Width(max(1, m.width)).Render(strings.Join(parts, " | "))
}

func (m Model) approvalPanel() string {
	if m.approval == nil {
		return ""
	}
	req := m.approval.request
	var b strings.Builder
	b.WriteString("Approve tool call")
	if req.ToolName != "" {
		b.WriteString(": ")
		b.WriteString(req.ToolName)
	}
	if req.Summary != "" {
		b.WriteString("\n")
		b.WriteString(req.Summary)
	}
	if req.Details != "" {
		b.WriteString("\n")
		b.WriteString(req.Details)
	}
	b.WriteString("\n[y] approve  [n/esc] deny")
	return approvalStyle.Width(max(1, m.width)).Render(b.String())
}

func startSubmitCmd(submit SubmitFunc, parent, ctx context.Context, prompt string, updates chan tea.Msg, enableApproval bool) tea.Cmd {
	return func() tea.Msg {
		go func() {
			defer close(updates)
			if submit == nil {
				updates <- submitDoneMsg{err: fmt.Errorf("submit function is not configured")}
				return
			}
			var approver agent.Approver
			if enableApproval {
				approver = tuiApprover{updates: updates}
			}
			result, err := submit(ctx, prompt, func(event agent.Event) {
				if event.Type == agent.EventTextDelta && event.Text == "" {
					return
				}
				select {
				case updates <- agentEventMsg(event):
				case <-ctx.Done():
				}
			}, approver)
			select {
			case updates <- submitDoneMsg{result: result, err: err}:
			case <-parent.Done():
			}
		}()
		return nil
	}
}

type tuiApprover struct {
	updates chan tea.Msg
}

func (a tuiApprover) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	response := make(chan approvalResponse, 1)
	select {
	case a.updates <- approvalRequestMsg{request: req, response: response}:
	case <-ctx.Done():
		return agent.ApprovalDecision{}, ctx.Err()
	}
	select {
	case result := <-response:
		return result.decision, result.err
	case <-ctx.Done():
		return agent.ApprovalDecision{}, ctx.Err()
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
	toolLabelStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	toolBodyStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	mutedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	approvalStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("236")).Padding(0, 1)
	statusStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("238"))
)

const toolLogPreviewLimit = 600
