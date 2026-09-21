package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/contextmgr"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/tools"
)

type Options struct {
	Config          config.Config
	ProviderFactory ProviderFactory
	Store           *session.Store
	History         []agent.Message
	Summary         *session.SummaryEntry
	Skills          skills.Set
	ModelRecorded   bool
}

// Service owns the mutable state of one conversation. Mutating operations are
// serialized so the same service can safely be used by terminal, desktop, and
// network transports.
type Service struct {
	mu              sync.Mutex
	cfg             config.Config
	providerFactory ProviderFactory
	store           *session.Store
	history         []agent.Message
	summary         *session.SummaryEntry
	skillSet        skills.Set
	modelRecorded   bool
	queue           *agent.MessageQueue
}

func NewService(options Options) *Service {
	return &Service{
		cfg:             options.Config,
		providerFactory: options.ProviderFactory,
		store:           options.Store,
		history:         append([]agent.Message(nil), options.History...),
		summary:         cloneSummaryEntry(options.Summary),
		skillSet:        options.Skills,
		modelRecorded:   options.ModelRecorded,
		queue:           &agent.MessageQueue{},
	}
}

func (s *Service) Queue() *agent.MessageQueue {
	return s.queue
}

func (s *Service) HasSession() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store != nil
}

func (s *Service) Run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.run(ctx, prompt, onEvent, approver)
	result.TreeItems = treeItems(s.store)
	return result, err
}

func (s *Service) run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	if result, ok, err := s.handleControlCommand(ctx, prompt); ok || err != nil {
		return result, err
	}
	preparedPrompt, err := preparePrompt(prompt, s.skillSet)
	if err != nil {
		return Result{}, err
	}
	if err := s.ensureModelRecorded(); err != nil {
		return Result{}, err
	}
	systemMessages, err := s.systemMessages()
	if err != nil {
		return Result{}, err
	}
	if err := s.recoverPendingTools(); err != nil {
		return Result{}, err
	}
	user := agent.Message{Role: agent.RoleUser, Content: preparedPrompt, Timestamp: time.Now().UnixMilli()}
	if err := s.persistMessage(user); err != nil {
		return Result{}, err
	}
	if s.providerFactory == nil {
		return Result{}, fmt.Errorf("provider factory is not configured")
	}
	provider := s.providerFactory(s.cfg)
	summaryUsage := agent.Usage{}
	runner := agent.NewRunnerWithOptions(provider, defaultTools(s.cfg, provider, s.skillSet.ReadRoots()), agent.RunnerOptions{
		Approver:      approver,
		OnMessage:     s.persistMessage,
		DrainMessages: s.queue.DrainSteering,
		BeforeRequest: func(ctx context.Context, req agent.Request) (agent.Request, error) {
			prepared, usage, err := s.prepareRequest(ctx, provider, systemMessages, req)
			summaryUsage = summaryUsage.Add(usage)
			return prepared, err
		},
	})
	reply, runErr := runner.Run(ctx, nil, onEvent)
	usage := summaryUsage.Add(runner.Usage())
	persistErr := appendUsage(s.store, runner.Usage())
	return Result{Content: reply.Content, Usage: usage, ModelName: s.cfg.Selection}, errors.Join(runErr, persistErr)
}

func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) snapshotLocked() Snapshot {
	loaded := session.Loaded{}
	path := ""
	if s.store != nil {
		loaded = s.store.State()
		path = s.store.Path()
	}
	summary := ""
	through := 0
	if s.summary != nil {
		summary = s.summary.Summary
		through = s.summary.ThroughMessageCount
	}
	return Snapshot{
		SessionID:      loaded.Header.ID,
		SessionName:    sessionName(loaded),
		SessionPath:    path,
		ModelName:      s.cfg.Selection,
		Summary:        summary,
		SummaryThrough: through,
		Messages:       append([]agent.Message{}, s.history...),
		TreeItems:      treeItems(s.store),
	}
}

func (s *Service) RenameSession(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return fmt.Errorf("session persistence is disabled")
	}
	return s.store.AppendName(name)
}

func (s *Service) handleControlCommand(ctx context.Context, prompt string) (Result, bool, error) {
	if result, ok, err := s.handleModelCommand(prompt); ok || err != nil {
		return result, ok, err
	}
	if result, ok, err := s.handleMemoryCommand(prompt); ok || err != nil {
		return result, ok, err
	}
	if ok, err := parseNoArgCommand(prompt, "/compact"); ok || err != nil {
		if err != nil {
			return Result{}, true, err
		}
		if err := s.ensureModelRecorded(); err != nil {
			return Result{}, true, err
		}
		if s.providerFactory == nil {
			return Result{}, true, fmt.Errorf("provider factory is not configured")
		}
		compact, err := s.compactHistory(ctx, s.providerFactory(s.cfg))
		if err != nil {
			return Result{}, true, err
		}
		return Result{Content: compact.message, Usage: compact.usage, ModelName: s.cfg.Selection}, true, nil
	}
	if ok, err := parseNoArgCommand(prompt, "/context"); ok || err != nil {
		if err != nil {
			return Result{}, true, err
		}
		return Result{Content: s.contextStatus(), ModelName: s.cfg.Selection}, true, nil
	}
	return Result{}, false, nil
}

func (s *Service) handleModelCommand(prompt string) (Result, bool, error) {
	arg, ok, err := parseModelCommand(prompt)
	if !ok || err != nil {
		return Result{}, ok, err
	}
	if arg == "" {
		return Result{Content: s.modelListText(), ModelName: s.cfg.Selection}, true, nil
	}
	next, err := s.cfg.WithSelection(arg)
	if err != nil {
		return Result{}, true, err
	}
	s.cfg = next
	if err := s.appendCurrentModel(); err != nil {
		return Result{}, true, err
	}
	s.modelRecorded = true
	return Result{Content: "model switched to " + s.cfg.Selection, ModelName: s.cfg.Selection}, true, nil
}

func (s *Service) ensureModelRecorded() error {
	if s.modelRecorded {
		return nil
	}
	if err := s.appendCurrentModel(); err != nil {
		return err
	}
	s.modelRecorded = true
	return nil
}

func (s *Service) appendCurrentModel() error {
	if s.store == nil {
		return nil
	}
	return s.store.AppendModel(s.cfg.Provider, s.cfg.Model)
}

func (s *Service) modelListText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "current model: %s", s.cfg.Selection)
	selections := s.cfg.AvailableSelections()
	if len(selections) == 0 {
		return b.String()
	}
	b.WriteString("\navailable models:")
	for _, selection := range selections {
		fmt.Fprintf(&b, "\n- %s", selection)
	}
	return b.String()
}

func (s *Service) handleMemoryCommand(prompt string) (Result, bool, error) {
	command, arg, ok, err := parseMemoryCommand(prompt)
	if !ok || err != nil {
		return Result{}, ok, err
	}
	if !s.cfg.Memory.Enabled {
		return Result{}, true, fmt.Errorf("memory is disabled")
	}
	if command == "" {
		status, err := memory.Status(s.cfg.MemoryPath, s.cfg.Memory.MaxPromptTokens, s.cfg.Memory.Enabled)
		return Result{Content: status, ModelName: s.cfg.Selection}, true, err
	}
	switch command {
	case "add":
		if err := memory.Append(s.cfg.MemoryPath, arg); err != nil {
			return Result{}, true, err
		}
		return Result{Content: "memory added", ModelName: s.cfg.Selection}, true, nil
	case "show":
		content, err := memory.Show(s.cfg.MemoryPath)
		return Result{Content: content, ModelName: s.cfg.Selection}, true, err
	default:
		return Result{}, true, fmt.Errorf("usage: /memory [add <text>|show]")
	}
}

type compactResult struct {
	message string
	usage   agent.Usage
}

func (s *Service) buildContext(systemMessages []agent.Message, user agent.Message) contextmgr.BuildResult {
	return contextmgr.Build(contextmgr.BuildInput{
		System:  systemMessages,
		History: s.history,
		Current: user,
		Summary: s.summaryState(),
		Config:  s.cfg.Context,
	})
}

func (s *Service) summaryState() contextmgr.SummaryState {
	if s.summary == nil {
		return contextmgr.SummaryState{}
	}
	through := s.summary.ThroughMessageCount
	if through < 0 || through > len(s.history) {
		through = 0
	}
	return contextmgr.SummaryState{Text: s.summary.Summary, ThroughMessageCount: through}
}

func (s *Service) compactHistory(ctx context.Context, provider agent.Provider) (compactResult, error) {
	_, through, _ := contextmgr.SummarizePrefix(s.history, s.summaryState(), s.cfg.Context.TailTurns)
	return s.compactThrough(ctx, provider, through)
}

func (s *Service) contextStatus() string {
	system, err := s.systemMessages()
	if err != nil {
		return "context: " + err.Error()
	}
	build := s.buildContext(system, agent.Message{})
	var defs []agent.ToolDefinition
	for _, tool := range defaultTools(s.cfg, nil, s.skillSet.ReadRoots()) {
		defs = append(defs, tool.Definition())
	}
	hasSummary := s.summary != nil && strings.TrimSpace(s.summary.Summary) != ""
	return fmt.Sprintf(
		"context: promptTokens=%d maxPromptTokens=%d tailTurns=%d summary=%t autoCompact=%t maxOutputTokens=%d",
		build.PromptTokens+contextmgr.EstimateTools(defs),
		s.cfg.Context.MaxPromptTokens,
		s.cfg.Context.TailTurns,
		hasSummary,
		s.cfg.Context.AutoCompact,
		s.cfg.Context.MaxOutputTokens,
	)
}

func (s *Service) systemMessages() ([]agent.Message, error) {
	messages, err := s.instructionMessages()
	if err != nil {
		return nil, err
	}
	messages = append(messages, skillSystemMessages(s.skillSet)...)
	if !s.cfg.Memory.Enabled {
		return messages, nil
	}
	snapshot, err := memory.Load(s.cfg.MemoryPath, s.cfg.Memory.MaxPromptTokens)
	if err != nil {
		return nil, err
	}
	prompt := memory.SystemPrompt(snapshot)
	if prompt != "" {
		messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: prompt, Timestamp: time.Now().UnixMilli()})
	}
	return messages, nil
}

func defaultTools(cfg config.Config, provider agent.Provider, readRoots []string) []agent.Tool {
	toolset := []agent.Tool{
		tools.NewReadToolWithOptions(cfg.CWD, tools.ReadOptions{ExtraRoots: readRoots}),
		tools.NewListTool(cfg.CWD),
		tools.NewGrepTool(cfg.CWD),
		tools.NewBashTool(cfg.CWD, tools.BashOptions{}),
		tools.NewEditTool(cfg.CWD),
		tools.NewWriteTool(cfg.CWD),
		tools.NewSubagentTool(cfg.CWD, provider, tools.SubagentOptions{}),
	}
	if cfg.Memory.Enabled {
		toolset = append(toolset, tools.NewMemoryAddTool(cfg.MemoryPath))
	}
	return toolset
}

func appendUsage(store *session.Store, usage agent.Usage) error {
	if store == nil {
		return nil
	}
	return store.AppendUsage(usage)
}

func skillSystemMessages(skillSet skills.Set) []agent.Message {
	prompt := skillSet.FormatSystemPrompt()
	if prompt == "" {
		return nil
	}
	return []agent.Message{{Role: agent.RoleSystem, Content: prompt, Timestamp: time.Now().UnixMilli()}}
}

func preparePrompt(prompt string, skillSet skills.Set) (string, error) {
	name, task, ok := parseSkillCommand(prompt)
	if !ok {
		return prompt, nil
	}
	skill, found := skillSet.Find(name)
	if !found {
		return "", fmt.Errorf("skill %q not found", name)
	}
	content, err := skills.ReadSkillFile(skill)
	if err != nil {
		return "", err
	}
	return skills.FormatForcedPrompt(skill, content, task), nil
}

func cloneSummaryEntry(entry *session.SummaryEntry) *session.SummaryEntry {
	if entry == nil {
		return nil
	}
	clone := *entry
	return &clone
}
