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
	ProviderFactory func(config.Config) agent.Provider
}

func Run(ctx context.Context, argv []string, options Options) int {
	stdout := writerOrDefault(options.Stdout, os.Stdout)
	stderr := writerOrDefault(options.Stderr, os.Stderr)
	stdin := readerOrDefault(options.Stdin, os.Stdin)

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
	executor := newTurnExecutor(cfg, providerFactory, sessionStore, loaded.Messages, skillSet, modelRecorded)

	if parsed.Prompt != "" {
		return runPrompt(ctx, executor, parsed.Prompt, stdout, stderr, false, parsed.Usage)
	}
	if parsed.Print {
		fmt.Fprintln(stderr, "prompt is required in print mode")
		return 2
	}
	if shouldRunTUI(stdin, stdout) {
		return runTUI(ctx, executor, cfg, stdin, stdout, stderr, parsed.Usage)
	}
	return runInteractive(ctx, executor, stdin, stdout, stderr, parsed.Usage)
}

func runPrompt(
	ctx context.Context,
	executor *turnExecutor,
	prompt string,
	stdout io.Writer,
	stderr io.Writer,
	stream bool,
	showUsage bool,
) int {
	var onDelta func(string)
	var streamed strings.Builder
	if stream {
		onDelta = func(text string) {
			streamed.WriteString(text)
			fmt.Fprint(stdout, text)
		}
	}
	result, err := executor.Run(ctx, prompt, onDelta)
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
	skillSet        skills.Set
	modelRecorded   bool
}

type agentProviderFactory func(config.Config) agent.Provider

func newTurnExecutor(cfg config.Config, providerFactory agentProviderFactory, store *session.Store, history []agent.Message, skillSet skills.Set, modelRecorded bool) *turnExecutor {
	return &turnExecutor{
		cfg:             cfg,
		providerFactory: providerFactory,
		store:           store,
		history:         append([]agent.Message(nil), history...),
		skillSet:        skillSet,
		modelRecorded:   modelRecorded,
	}
}

func (e *turnExecutor) Run(ctx context.Context, prompt string, onDelta func(string)) (turnResult, error) {
	if result, ok, err := e.handleModelCommand(prompt); ok || err != nil {
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
	messages := make([]agent.Message, 0, len(systemMessages)+len(e.history)+1)
	messages = append(messages, systemMessages...)
	messages = append(messages, e.history...)
	messages = append(messages, user)
	var onEvent func(agent.Event)
	if onDelta != nil {
		onEvent = func(event agent.Event) {
			if event.Type == agent.EventTextDelta {
				onDelta(event.Text)
			}
		}
	}
	provider := e.providerFactory(e.cfg)
	runner := agent.NewRunner(provider, defaultTools(e.cfg.CWD, provider, e.skillSet.ReadRoots()))
	reply, err := runner.Run(ctx, messages, onEvent)
	if err != nil {
		return turnResult{}, err
	}
	if err := appendNewMessages(e.store, runner.Transcript(), len(systemMessages)+len(e.history)); err != nil {
		return turnResult{}, err
	}
	usage := runner.Usage()
	if err := appendUsage(e.store, usage); err != nil {
		return turnResult{}, err
	}
	e.history = stripSystemMessages(runner.Transcript())
	return turnResult{Content: reply.Content, Usage: usage, ModelName: e.cfg.Selection}, nil
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
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	showUsage bool,
) int {
	fmt.Fprintln(stdout, "gg interactive mode. Press Ctrl+D to exit.")
	scanner := bufio.NewScanner(stdin)
	for {
		fmt.Fprint(stdout, "> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		code := runPrompt(ctx, executor, prompt, stdout, stderr, true, showUsage)
		if code != 0 {
			return code
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
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
) int {
	err := tui.Run(ctx, tui.Config{
		CWD:             cfg.CWD,
		ModelName:       cfg.Selection,
		ShowUsage:       showUsage,
		InitialMessages: displayMessages(executor.history),
		Input:           stdin,
		Output:          stdout,
		Submit: func(ctx context.Context, prompt string, onDelta func(string)) (tui.SubmitResult, error) {
			result, err := executor.Run(ctx, prompt, onDelta)
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

func printUsage(stderr io.Writer, usage agent.Usage) {
	fmt.Fprintf(stderr, "tokens: prompt=%d completion=%d total=%d\n", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
}

func shouldRunTUI(stdin io.Reader, stdout io.Writer) bool {
	return isTerminal(stdin) && isTerminal(stdout)
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
