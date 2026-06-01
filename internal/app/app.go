package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/contextmgr"
	"github.com/hszjj221/gg/internal/provider/openai"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/tools"
	"github.com/hszjj221/gg/internal/tui"
)

type Options struct {
	CWD             string
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	Version         string
	HomeDir         string
	IsTerminal      func(any) bool
	ProviderFactory func(config.Config) agent.Provider
}

func Run(ctx context.Context, argv []string, options Options) int {
	stdout := writerOrDefault(options.Stdout, os.Stdout)
	stderr := writerOrDefault(options.Stderr, os.Stderr)
	stdin := readerOrDefault(options.Stdin, os.Stdin)
	isTerm := terminalChecker(options)

	parsed, err := cli.Parse(argv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if parsed.Help {
		fmt.Fprintln(stdout, cli.HelpText())
		return 0
	}
	if parsed.Version {
		version := options.Version
		if version == "" {
			version = "dev"
		}
		fmt.Fprintln(stdout, version)
		return 0
	}

	cfg, err := config.Resolve(config.Options{
		APIKey:     parsed.APIKey,
		BaseURL:    parsed.BaseURL,
		Model:      parsed.Model,
		SessionDir: parsed.SessionDir,
		CWD:        options.CWD,
		HomeDir:    options.HomeDir,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := validateSessionArgs(parsed); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if parsed.Command == cli.CommandSessionsList {
		return runSessionsList(cfg, stdout, stderr)
	}
	if parsed.Approval == "on-request" && !bothTerminals(stdin, stdout, isTerm) {
		fmt.Fprintln(stderr, "--approval on-request requires a terminal")
		return 2
	}
	skillSet, err := loadSkills(parsed, cfg.CWD, options.HomeDir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	providerFactory := options.ProviderFactory
	if providerFactory == nil {
		providerFactory = func(cfg config.Config) agent.Provider {
			return openai.NewClient(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Model: cfg.Model})
		}
	}

	sessionStore, loaded, err := openSession(parsed, cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if parsed.Model == "" && loaded.LastModel != nil {
		cfg, err = cfg.WithSelection(loaded.LastModel.Selection)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	modelRecorded := loaded.LastModel != nil && loaded.LastModel.Selection == cfg.Selection
	executor := newTurnExecutor(cfg, providerFactory, sessionStore, loaded.Messages, loaded.LastSummary, skillSet, modelRecorded)

	if parsed.Prompt != "" {
		return runPrompt(ctx, executor, parsed.Prompt, stdout, stderr, false, parsed.Usage, promptApprover(parsed, stdin, stderr))
	}
	if parsed.Print {
		fmt.Fprintln(stderr, "prompt is required in print mode")
		return 2
	}
	if shouldRunTUI(stdin, stdout, isTerm) {
		return runTUI(ctx, executor, cfg, stdin, stdout, stderr, parsed.Usage, parsed.Approval != "never")
	}
	reader := bufio.NewReader(stdin)
	return runInteractive(ctx, executor, reader, stdout, stderr, parsed.Usage, interactiveApprover(parsed, reader, stderr, bothTerminals(stdin, stdout, isTerm)))
}

func runPrompt(
	ctx context.Context,
	executor *turnExecutor,
	prompt string,
	stdout io.Writer,
	stderr io.Writer,
	stream bool,
	showUsage bool,
	approver agent.Approver,
) int {
	var onDelta func(string)
	var onEvent func(agent.Event)
	var streamed strings.Builder
	if stream {
		onDelta = func(text string) {
			streamed.WriteString(text)
			fmt.Fprint(stdout, text)
		}
	}
	if onDelta != nil {
		onEvent = func(event agent.Event) {
			if event.Type == agent.EventTextDelta {
				onDelta(event.Text)
			}
		}
	}
	result, err := executor.Run(ctx, prompt, onEvent, approver)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if stream {
		if streamed.Len() == 0 && result.Content != "" {
			fmt.Fprint(stdout, result.Content)
		}
		fmt.Fprintln(stdout)
	} else {
		fmt.Fprintln(stdout, result.Content)
	}
	if showUsage {
		printUsage(stderr, result.Usage)
	}
	return 0
}

type turnResult struct {
	Content   string
	Usage     agent.Usage
	ModelName string
}

type turnExecutor struct {
	cfg             config.Config
	providerFactory agentProviderFactory
	store           *session.Store
	history         []agent.Message
	summary         *session.SummaryEntry
	skillSet        skills.Set
	modelRecorded   bool
}

type agentProviderFactory func(config.Config) agent.Provider

func newTurnExecutor(cfg config.Config, providerFactory agentProviderFactory, store *session.Store, history []agent.Message, summary *session.SummaryEntry, skillSet skills.Set, modelRecorded bool) *turnExecutor {
	return &turnExecutor{
		cfg:             cfg,
		providerFactory: providerFactory,
		store:           store,
		history:         append([]agent.Message(nil), history...),
		summary:         cloneSummaryEntry(summary),
		skillSet:        skillSet,
		modelRecorded:   modelRecorded,
	}
}

func (e *turnExecutor) Run(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (turnResult, error) {
	if result, ok, err := e.handleControlCommand(ctx, prompt); ok || err != nil {
		return result, err
	}
	preparedPrompt, err := preparePrompt(prompt, e.skillSet)
	if err != nil {
		return turnResult{}, err
	}
	if err := e.ensureModelRecorded(); err != nil {
		return turnResult{}, err
	}
	systemMessages := skillSystemMessages(e.skillSet)
	user := agent.Message{Role: agent.RoleUser, Content: preparedPrompt, Timestamp: time.Now().UnixMilli()}
	provider := e.providerFactory(e.cfg)
	summaryUsage := agent.Usage{}
	build := e.buildContext(systemMessages, user)
	if e.cfg.Context.AutoCompact && build.PromptTokens > e.cfg.Context.MaxPromptTokens {
		compact, err := e.compactHistory(ctx, provider)
		if err != nil {
			return turnResult{}, err
		}
		summaryUsage = summaryUsage.Add(compact.usage)
		build = e.buildContext(systemMessages, user)
	}
	runner := agent.NewRunnerWithOptions(provider, defaultTools(e.cfg.CWD, provider, e.skillSet.ReadRoots()), agent.RunnerOptions{Approver: approver})
	reply, err := runner.Run(ctx, build.Messages, onEvent)
	if err != nil {
		return turnResult{}, err
	}
	newMessages := runner.Transcript()[len(build.Messages)-1:]
	if err := appendNewMessages(e.store, newMessages, 0); err != nil {
		return turnResult{}, err
	}
	usage := summaryUsage.Add(runner.Usage())
	if err := appendUsage(e.store, usage); err != nil {
		return turnResult{}, err
	}
	e.history = append(e.history, stripSystemMessages(newMessages)...)
	return turnResult{Content: reply.Content, Usage: usage, ModelName: e.cfg.Selection}, nil
}

func (e *turnExecutor) handleControlCommand(ctx context.Context, prompt string) (turnResult, bool, error) {
	if result, ok, err := e.handleModelCommand(prompt); ok || err != nil {
		return result, ok, err
	}
	if ok, err := parseNoArgCommand(prompt, "/compact"); ok || err != nil {
		if err != nil {
			return turnResult{}, true, err
		}
		if err := e.ensureModelRecorded(); err != nil {
			return turnResult{}, true, err
		}
		compact, err := e.compactHistory(ctx, e.providerFactory(e.cfg))
		if err != nil {
			return turnResult{}, true, err
		}
		if err := appendUsage(e.store, compact.usage); err != nil {
			return turnResult{}, true, err
		}
		return turnResult{Content: compact.message, Usage: compact.usage, ModelName: e.cfg.Selection}, true, nil
	}
	if ok, err := parseNoArgCommand(prompt, "/context"); ok || err != nil {
		if err != nil {
			return turnResult{}, true, err
		}
		return turnResult{Content: e.contextStatus(), ModelName: e.cfg.Selection}, true, nil
	}
	return turnResult{}, false, nil
}

func (e *turnExecutor) handleModelCommand(prompt string) (turnResult, bool, error) {
	arg, ok, err := parseModelCommand(prompt)
	if !ok || err != nil {
		return turnResult{}, ok, err
	}
	if arg == "" {
		return turnResult{Content: e.modelListText(), ModelName: e.cfg.Selection}, true, nil
	}
	next, err := e.cfg.WithSelection(arg)
	if err != nil {
		return turnResult{}, true, err
	}
	e.cfg = next
	if err := e.appendCurrentModel(); err != nil {
		return turnResult{}, true, err
	}
	e.modelRecorded = true
	return turnResult{Content: "model switched to " + e.cfg.Selection, ModelName: e.cfg.Selection}, true, nil
}

func (e *turnExecutor) ensureModelRecorded() error {
	if e.modelRecorded {
		return nil
	}
	if err := e.appendCurrentModel(); err != nil {
		return err
	}
	e.modelRecorded = true
	return nil
}

func (e *turnExecutor) appendCurrentModel() error {
	if e.store == nil {
		return nil
	}
	return e.store.AppendModel(e.cfg.Provider, e.cfg.Model)
}

func (e *turnExecutor) modelListText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "current model: %s", e.cfg.Selection)
	selections := e.cfg.AvailableSelections()
	if len(selections) == 0 {
		return b.String()
	}
	b.WriteString("\navailable models:")
	for _, selection := range selections {
		fmt.Fprintf(&b, "\n- %s", selection)
	}
	return b.String()
}

type compactResult struct {
	message string
	usage   agent.Usage
}

func (e *turnExecutor) buildContext(systemMessages []agent.Message, user agent.Message) contextmgr.BuildResult {
	return contextmgr.Build(contextmgr.BuildInput{
		System:  systemMessages,
		History: e.history,
		Current: user,
		Summary: e.summaryState(),
		Config:  e.cfg.Context,
	})
}

func (e *turnExecutor) summaryState() contextmgr.SummaryState {
	if e.summary == nil {
		return contextmgr.SummaryState{}
	}
	through := e.summary.ThroughMessageCount
	if through < 0 || through > len(e.history) {
		through = 0
	}
	return contextmgr.SummaryState{Text: e.summary.Summary, ThroughMessageCount: through}
}

func (e *turnExecutor) compactHistory(ctx context.Context, provider agent.Provider) (compactResult, error) {
	messages, through, keptTurns := contextmgr.SummarizePrefix(e.history, e.summaryState(), e.cfg.Context.TailTurns)
	if len(messages) == 0 {
		return compactResult{message: fmt.Sprintf("context compacted: summarized 0 messages, kept %d turns", keptTurns)}, nil
	}
	prompt := contextmgr.FormatSummaryPrompt(e.summaryState().Text, messages, e.cfg.Context.SummaryMaxTokens)
	reply, err := provider.Complete(ctx, agent.Request{Messages: []agent.Message{
		{Role: agent.RoleSystem, Content: "You compact conversation history for a coding agent."},
		{Role: agent.RoleUser, Content: prompt},
	}}, nil)
	if err != nil {
		return compactResult{}, fmt.Errorf("context compaction failed: %w", err)
	}
	summary := strings.TrimSpace(reply.Content)
	if summary == "" {
		return compactResult{}, fmt.Errorf("context compaction returned empty summary")
	}
	if e.store != nil {
		if err := e.store.AppendSummary(summary, through); err != nil {
			return compactResult{}, err
		}
	}
	e.summary = &session.SummaryEntry{Summary: summary, ThroughMessageCount: through}
	return compactResult{
		message: fmt.Sprintf("context compacted: summarized %d messages, kept %d turns", len(messages), keptTurns),
		usage:   reply.Usage,
	}, nil
}

func (e *turnExecutor) contextStatus() string {
	build := e.buildContext(skillSystemMessages(e.skillSet), agent.Message{Role: agent.RoleUser})
	hasSummary := e.summary != nil && strings.TrimSpace(e.summary.Summary) != ""
	return fmt.Sprintf(
		"context: promptTokens=%d maxPromptTokens=%d tailTurns=%d summary=%t autoCompact=%t",
		build.PromptTokens,
		e.cfg.Context.MaxPromptTokens,
		e.cfg.Context.TailTurns,
		hasSummary,
		e.cfg.Context.AutoCompact,
	)
}

func defaultTools(cwd string, provider agent.Provider, readRoots []string) []agent.Tool {
	return []agent.Tool{
		tools.NewReadToolWithOptions(cwd, tools.ReadOptions{ExtraRoots: readRoots}),
		tools.NewListTool(cwd),
		tools.NewGrepTool(cwd),
		tools.NewBashTool(cwd, tools.BashOptions{}),
		tools.NewEditTool(cwd),
		tools.NewWriteTool(cwd),
		tools.NewSubagentTool(cwd, provider, tools.SubagentOptions{}),
	}
}

func validateSessionArgs(args cli.Args) error {
	if args.NoSession && (args.Command == cli.CommandResume || args.Continue || args.Last) {
		return fmt.Errorf("--no-session cannot be used with resume, --continue, or --last")
	}
	if args.Session != "" && (args.Command == cli.CommandResume || args.Continue || args.Last) {
		return fmt.Errorf("--session cannot be combined with resume, --continue, or --last")
	}
	return nil
}

func runSessionsList(cfg config.Config, stdout io.Writer, stderr io.Writer) int {
	infos, err := session.ListForCWD(cfg.SessionDir, cfg.CWD)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(infos) == 0 {
		fmt.Fprintln(stdout, "no sessions found")
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUPDATED\tMESSAGES\tPATH\tPREVIEW")
	for _, info := range infos {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", info.ID, info.Timestamp, info.MessageCount, info.Path, info.Preview)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runInteractive(
	ctx context.Context,
	executor *turnExecutor,
	stdin *bufio.Reader,
	stdout io.Writer,
	stderr io.Writer,
	showUsage bool,
	approver agent.Approver,
) int {
	fmt.Fprintln(stdout, "gg interactive mode. Press Ctrl+D to exit.")
	for {
		fmt.Fprint(stdout, "> ")
		line, err := stdin.ReadString('\n')
		if err == io.EOF && line == "" {
			break
		}
		if err != nil && err != io.EOF {
			fmt.Fprintln(stderr, err)
			return 1
		}
		prompt := strings.TrimSpace(line)
		if prompt == "" {
			if err == io.EOF {
				break
			}
			continue
		}
		code := runPrompt(ctx, executor, prompt, stdout, stderr, true, showUsage, approver)
		if code != 0 {
			return code
		}
		if err == io.EOF {
			break
		}
	}
	return 0
}

func runTUI(
	ctx context.Context,
	executor *turnExecutor,
	cfg config.Config,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	showUsage bool,
	enableApproval bool,
) int {
	err := tui.Run(ctx, tui.Config{
		CWD:             cfg.CWD,
		ModelName:       cfg.Selection,
		ShowUsage:       showUsage,
		EnableApproval:  enableApproval,
		InitialMessages: displayMessages(executor.history),
		Input:           stdin,
		Output:          stdout,
		Submit: func(ctx context.Context, prompt string, onEvent func(agent.Event), approver agent.Approver) (tui.SubmitResult, error) {
			result, err := executor.Run(ctx, prompt, onEvent, approver)
			return tui.SubmitResult{Content: result.Content, Usage: result.Usage, ModelName: result.ModelName}, err
		},
	})
	if err != nil && err != context.Canceled {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func appendNewMessages(store *session.Store, transcript []agent.Message, skip int) error {
	if store == nil {
		return nil
	}
	for _, msg := range transcript[skip:] {
		if err := store.AppendMessage(msg); err != nil {
			return err
		}
	}
	return nil
}

func appendUsage(store *session.Store, usage agent.Usage) error {
	if store == nil {
		return nil
	}
	return store.AppendUsage(usage)
}

func loadSkills(args cli.Args, cwd, homeDir string) (skills.Set, error) {
	if args.NoSkills {
		return skills.Set{}, nil
	}
	return skills.Load(skills.LoadOptions{CWD: cwd, HomeDir: homeDir})
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

func parseModelCommand(prompt string) (arg string, ok bool, err error) {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 || fields[0] != "/model" {
		return "", false, nil
	}
	if len(fields) > 2 {
		return "", true, fmt.Errorf("usage: /model [provider:model]")
	}
	if len(fields) == 1 {
		return "", true, nil
	}
	return fields[1], true, nil
}

func parseNoArgCommand(prompt, command string) (bool, error) {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 || fields[0] != command {
		return false, nil
	}
	if len(fields) > 1 {
		return true, fmt.Errorf("usage: %s", command)
	}
	return true, nil
}

func parseSkillCommand(prompt string) (name, task string, ok bool) {
	trimmed := strings.TrimSpace(prompt)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/skill:") {
		return "", "", false
	}
	name = strings.TrimPrefix(fields[0], "/skill:")
	return name, strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0])), true
}

func stripSystemMessages(messages []agent.Message) []agent.Message {
	out := make([]agent.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role != agent.RoleSystem {
			out = append(out, message)
		}
	}
	return out
}

func cloneSummaryEntry(entry *session.SummaryEntry) *session.SummaryEntry {
	if entry == nil {
		return nil
	}
	clone := *entry
	return &clone
}

func printUsage(stderr io.Writer, usage agent.Usage) {
	fmt.Fprintf(stderr, "tokens: prompt=%d completion=%d total=%d\n", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
}

type lineApprover struct {
	reader *bufio.Reader
	stderr io.Writer
}

func newLineApprover(reader *bufio.Reader, stderr io.Writer) agent.Approver {
	return lineApprover{reader: reader, stderr: stderr}
}

func (a lineApprover) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	fmt.Fprintf(a.stderr, "\nApprove tool call: %s\n", req.ToolName)
	if req.Summary != "" {
		fmt.Fprintf(a.stderr, "%s\n", req.Summary)
	}
	if req.Details != "" {
		fmt.Fprintf(a.stderr, "%s\n", req.Details)
	}
	fmt.Fprint(a.stderr, "Approve? [y/N] ")
	answer, err := a.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return agent.ApprovalDecision{}, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return agent.ApprovalDecision{Allow: answer == "y" || answer == "yes"}, nil
}

func promptApprover(args cli.Args, stdin io.Reader, stderr io.Writer) agent.Approver {
	if args.Approval != "on-request" {
		return nil
	}
	return newLineApprover(bufio.NewReader(stdin), stderr)
}

func interactiveApprover(args cli.Args, reader *bufio.Reader, stderr io.Writer, terminal bool) agent.Approver {
	switch args.Approval {
	case "never":
		return nil
	case "on-request":
		return newLineApprover(reader, stderr)
	case "auto":
		if terminal {
			return newLineApprover(reader, stderr)
		}
	}
	return nil
}

func shouldRunTUI(stdin io.Reader, stdout io.Writer, isTerm func(any) bool) bool {
	return bothTerminals(stdin, stdout, isTerm)
}

func bothTerminals(stdin io.Reader, stdout io.Writer, isTerm func(any) bool) bool {
	return isTerm(stdin) && isTerm(stdout)
}

func terminalChecker(options Options) func(any) bool {
	if options.IsTerminal != nil {
		return options.IsTerminal
	}
	return isTerminal
}

func isTerminal(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func displayMessages(messages []agent.Message) []tui.Message {
	out := make([]tui.Message, 0, len(messages))
	for _, message := range messages {
		if message.Content == "" {
			continue
		}
		switch message.Role {
		case agent.RoleUser, agent.RoleAssistant:
			out = append(out, tui.Message{Role: message.Role, Content: message.Content})
		}
	}
	return out
}

func openSession(args cli.Args, cfg config.Config) (*session.Store, session.Loaded, error) {
	if args.NoSession {
		return nil, session.Loaded{}, nil
	}
	path := args.Session
	switch {
	case args.Command == cli.CommandResume:
		resolved, err := session.FindForCWD(cfg.SessionDir, cfg.CWD, args.ResumeTarget)
		if err != nil {
			return nil, session.Loaded{}, err
		}
		path = resolved
	case args.Continue || args.Last:
		latest, err := session.LatestForCWD(cfg.SessionDir, cfg.CWD)
		if err != nil {
			return nil, session.Loaded{}, err
		}
		path = latest.Path
	}
	if path == "" {
		path = defaultSessionPath(cfg.SessionDir, cfg.CWD)
	}
	store, err := session.NewStore(path, cfg.CWD)
	if err != nil {
		return nil, session.Loaded{}, err
	}
	loaded, err := session.Load(store.Path())
	if err != nil {
		return nil, session.Loaded{}, err
	}
	return store, loaded, nil
}

func defaultSessionPath(sessionDir, cwd string) string {
	filename := fmt.Sprintf("%d.jsonl", time.Now().UnixNano())
	return filepath.Join(session.CWDDir(sessionDir, cwd), filename)
}

func writerOrDefault(w io.Writer, fallback io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return fallback
}

func readerOrDefault(r io.Reader, fallback io.Reader) io.Reader {
	if r != nil {
		return r
	}
	return fallback
}
