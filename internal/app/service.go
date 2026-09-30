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
	"github.com/hszjj221/gg/internal/userprofile"
)

type Options struct {
	Config          config.Config
	ProviderFactory ProviderFactory
	Store           *session.Store
	History         []agent.Message
	Summary         *session.SummaryEntry
	Skills          skills.Set
	ModelRecorded   bool
	// Profile and MemoryStore carry the personal layer. MemoryStore may be
	// nil; NewService then falls back to a store rooted at Config.Memory.Dir.
	Profile     userprofile.Profile
	MemoryStore *memory.Store
}

// Service owns the mutable state of one conversation. Mutating operations are
// serialized so the same service can safely be used by terminal, desktop, and
// network transports.
//
// Locking: mu guards cfg, store, history, summary, modelRecorded, and
// toolMemo. It is held only for short, non-blocking critical sections —
// never across provider network calls, tool execution, or approval. The
// Manager serializes Run per session, so all mutations happen on the Run
// goroutine; the lock exists for concurrent readers (Snapshot, session
// actions) during a turn.
type Service struct {
	mu              sync.Mutex
	cfg             config.Config
	providerFactory ProviderFactory
	store           *session.Store
	history         []agent.Message
	summary         *session.SummaryEntry
	skillSet        skills.Set
	modelRecorded   bool
	profile         userprofile.Profile
	memStore        *memory.Store
	queue           *agent.MessageQueue
	// browserPool scopes one Chromium session to this Service (one
	// conversation). It is created once here — not per turn — and closed
	// via Close when the Service is retired, so daemon turns cannot leak
	// headless Chromium processes.
	browserPool *tools.BrowserSessionPool
	// toolMemo caches per-Service tool resources (Chromium probe, shared
	// media client) so turns don't redo static work. It synchronizes
	// itself; callers don't need s.mu.
	toolMemo *ToolMemo
	// running is true while a turn is in flight. Session actions that
	// replace the store and history (checkout, fork-switch, clone-switch)
	// are rejected while it is set: the runner keeps persisting into the
	// state the turn started with, so swapping underneath it would land
	// the turn's messages on the wrong branch.
	running bool
}

func NewService(options Options) *Service {
	memStore := options.MemoryStore
	if memStore == nil {
		memStore = memory.NewStore(options.Config.Memory.Dir)
	}
	return &Service{
		cfg:             options.Config,
		providerFactory: options.ProviderFactory,
		store:           options.Store,
		history:         append([]agent.Message(nil), options.History...),
		summary:         cloneSummaryEntry(options.Summary),
		skillSet:        options.Skills,
		modelRecorded:   options.ModelRecorded,
		profile:         options.Profile,
		memStore:        memStore,
		queue:           &agent.MessageQueue{},
		browserPool:     tools.NewBrowserSessionPool(),
		toolMemo:        &ToolMemo{},
	}
}

// buildTools assembles the agent toolset for one turn via the provider
// registry. Static resources (Chromium probe, media client) are memoized
// per Service; dynamic state (connector token, model selection, config)
// is re-read every turn so mid-session changes take effect.
func (s *Service) buildTools(ctx context.Context, provider agent.Provider) []agent.Tool {
	loc, tzErr := userLocation(s.profile)
	tc := ToolContext{
		Config:      s.cfg,
		Provider:    provider,
		ReadRoots:   s.skillSet.ReadRoots(),
		MemStore:    s.memStore,
		Location:    loc,
		LocationErr: tzErr,
		BrowserPool: s.browserPool,
		Memoized:    s.toolMemo,
	}
	var out []agent.Tool
	for _, p := range toolProviders {
		built, err := p.Build(ctx, tc)
		if err != nil {
			// A failing provider degrades to absent for this turn; the
			// rest of the toolset keeps working.
			continue
		}
		out = append(out, built...)
	}
	return out
}

func (s *Service) Queue() *agent.MessageQueue {
	return s.queue
}

// Close releases resources held by the Service, currently the shared
// Chromium session behind the browser tools. It is idempotent and safe to
// call on a Service whose browser tools never started Chromium.
func (s *Service) Close() error {
	// Both closings are best-effort and idempotent; report the first error.
	if err := s.browserPool.Close(); err != nil {
		_ = s.toolMemo.Close()
		return err
	}
	return s.toolMemo.Close()
}

func (s *Service) HasSession() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store != nil
}

func (s *Service) Run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	result, err := s.run(ctx, prompt, onEvent, approver)
	result.TreeItems = s.treeItems()
	return result, err
}

// treeItems reads the current store under mu; session.Store serializes its
// own record access, so the lock is released before walking the tree.
func (s *Service) treeItems() []TreeItem {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	return treeItems(store)
}

func (s *Service) run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (Result, error) {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
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
	runner := agent.NewRunnerWithOptions(provider, s.buildTools(ctx, provider), agent.RunnerOptions{
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
	persistErr := s.appendUsage(runner.Usage())
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
		return Result{Content: s.contextStatus(ctx), ModelName: s.cfg.Selection}, true, nil
	}
	return Result{}, false, nil
}

func (s *Service) handleModelCommand(prompt string) (Result, bool, error) {
	arg, ok, err := parseModelCommand(prompt)
	if !ok || err != nil {
		return Result{}, ok, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if arg == "" {
		return Result{Content: s.modelListTextLocked(), ModelName: s.cfg.Selection}, true, nil
	}
	next, err := s.cfg.WithSelection(arg)
	if err != nil {
		return Result{}, true, err
	}
	s.cfg = next
	if err := s.appendCurrentModelLocked(); err != nil {
		return Result{}, true, err
	}
	s.modelRecorded = true
	return Result{Content: "model switched to " + s.cfg.Selection, ModelName: s.cfg.Selection}, true, nil
}

func (s *Service) ensureModelRecorded() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modelRecorded {
		return nil
	}
	if err := s.appendCurrentModelLocked(); err != nil {
		return err
	}
	s.modelRecorded = true
	return nil
}

func (s *Service) appendCurrentModel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendCurrentModelLocked()
}

// appendCurrentModelLocked requires s.mu to be held. session.Store
// serializes its own appends, so taking its lock under s.mu is safe.
func (s *Service) appendCurrentModelLocked() error {
	if s.store == nil {
		return nil
	}
	return s.store.AppendModel(s.cfg.Provider, s.cfg.Model)
}

func (s *Service) modelListText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelListTextLocked()
}

// modelListTextLocked requires s.mu to be held.
func (s *Service) modelListTextLocked() string {
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
	store := s.memStore
	if command == "" {
		status, err := memory.Status(store.CuratedPath(), s.cfg.Memory.MaxPromptTokens, s.cfg.Memory.Enabled)
		return Result{Content: status, ModelName: s.cfg.Selection}, true, err
	}
	switch command {
	case "add":
		scope, text := parseScopeFlag(arg)
		if strings.TrimSpace(text) == "" {
			return Result{}, true, fmt.Errorf("usage: /memory add [--scope=SCOPE] <text>")
		}
		var werr error
		switch {
		case scope == "" || scope == "general":
			werr = store.AppendCurated(text)
		case scope == "daily":
			werr = store.AppendDaily(text)
		case strings.HasPrefix(scope, "person:"):
			name := strings.TrimSpace(strings.TrimPrefix(scope, "person:"))
			if name == "" {
				return Result{}, true, fmt.Errorf("person scope needs a name: /memory add --scope=person:<name> <text>")
			}
			werr = store.AppendPerson(name, text)
		case strings.HasPrefix(scope, "group:"):
			name := strings.TrimSpace(strings.TrimPrefix(scope, "group:"))
			if name == "" {
				return Result{}, true, fmt.Errorf("group scope needs a name: /memory add --scope=group:<name> <text>")
			}
			werr = store.AppendGroup(name, text)
		default:
			return Result{}, true, fmt.Errorf("unknown scope %q: want general, daily, person:<name>, group:<name>", scope)
		}
		if werr != nil {
			return Result{}, true, werr
		}
		return Result{Content: "memory added", ModelName: s.cfg.Selection}, true, nil
	case "show":
		path := store.CuratedPath()
		if arg == "daily" {
			path = store.DailyPath(time.Now())
		}
		content, err := memory.Show(path)
		return Result{Content: content, ModelName: s.cfg.Selection}, true, err
	case "search":
		hits, err := store.Search(arg, "all")
		if err != nil {
			return Result{}, true, err
		}
		if len(hits) == 0 {
			return Result{Content: "no memory matches", ModelName: s.cfg.Selection}, true, nil
		}
		var b strings.Builder
		for _, hit := range hits {
			fmt.Fprintf(&b, "%s:%d: %s\n", hit.Path, hit.Line, hit.Snippet)
		}
		return Result{Content: strings.TrimSpace(b.String()), ModelName: s.cfg.Selection}, true, nil
	default:
		return Result{}, true, fmt.Errorf("usage: /memory [add [--scope=SCOPE] <text>|show [daily]|search <query>]")
	}
}

// parseScopeFlag splits a leading "--scope=SCOPE" from /memory add arguments.
func parseScopeFlag(arg string) (scope, text string) {
	if head, rest, ok := strings.Cut(arg, " "); ok && strings.HasPrefix(head, "--scope=") {
		return strings.TrimPrefix(head, "--scope="), strings.TrimSpace(rest)
	}
	// A bare "--scope=X" with no text is a usage error, not literal content.
	if strings.HasPrefix(arg, "--scope=") {
		return strings.TrimPrefix(arg, "--scope="), ""
	}
	return "", arg
}

type compactResult struct {
	message string
	usage   agent.Usage
}

func (s *Service) buildContext(systemMessages []agent.Message, user agent.Message) contextmgr.BuildResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildContextLocked(systemMessages, user)
}

// buildContextLocked requires s.mu to be held. It is CPU-only: estimation
// and message cloning, no I/O.
func (s *Service) buildContextLocked(systemMessages []agent.Message, user agent.Message) contextmgr.BuildResult {
	return contextmgr.Build(contextmgr.BuildInput{
		System:  systemMessages,
		History: s.history,
		Current: user,
		Summary: s.summaryStateLocked(),
		Config:  s.cfg.Context,
	})
}

func (s *Service) summaryState() contextmgr.SummaryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryStateLocked()
}

// summaryStateLocked requires s.mu to be held.
func (s *Service) summaryStateLocked() contextmgr.SummaryState {
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
	s.mu.Lock()
	history := s.history
	state := s.summaryStateLocked()
	tailTurns := s.cfg.Context.TailTurns
	s.mu.Unlock()
	_, through, _ := contextmgr.SummarizePrefix(history, state, tailTurns)
	return s.compactThrough(ctx, provider, through)
}

func (s *Service) contextStatus(ctx context.Context) string {
	system, err := s.systemMessages()
	if err != nil {
		return "context: " + err.Error()
	}
	build := s.buildContext(system, agent.Message{})
	var defs []agent.ToolDefinition
	for _, tool := range s.buildTools(ctx, nil) {
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
	// Prompt order: stable identity first, then memory, then project
	// context, then skills.
	messages := []agent.Message{s.codingInstructionMessage()}
	if block := s.profile.PromptBlock(); block != "" {
		messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: block})
	}
	if s.cfg.Memory.Enabled {
		snapshot, err := s.memStore.LoadCurated(s.cfg.Memory.MaxPromptTokens)
		if err != nil {
			return nil, err
		}
		if prompt := memory.SystemPrompt(snapshot); prompt != "" {
			messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: prompt, Timestamp: time.Now().UnixMilli()})
		}
		tail, err := s.memStore.LoadDailyTail(s.cfg.Memory.DailyLogTailTokens)
		if err != nil {
			return nil, err
		}
		if content := strings.TrimSpace(tail.Content); content != "" {
			messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: "Today's log:\n" + content, Timestamp: time.Now().UnixMilli()})
		}
	}
	project, err := s.projectInstructionMessages()
	if err != nil {
		return nil, err
	}
	messages = append(messages, project...)
	messages = append(messages, skillSystemMessages(s.skillSet)...)
	return messages, nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// userLocation resolves the user's timezone for calendar tools. When the
// profile timezone is set but not a valid IANA name, it returns the error
// so calendar tools refuse to run rather than silently interpreting
// wall-clock input in the wrong zone.
func userLocation(profile userprofile.Profile) (*time.Location, error) {
	if profile.Timezone != "" {
		if loc, err := time.LoadLocation(profile.Timezone); err == nil {
			return loc, nil
		}
		return time.Local, fmt.Errorf("profile timezone %q is not a valid IANA name; edit ~/.gg/USER.md to fix or unset it", profile.Timezone)
	}
	return time.Local, nil
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
