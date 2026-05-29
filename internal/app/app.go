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

	cfg := config.Resolve(config.Options{
		APIKey:     parsed.APIKey,
		BaseURL:    parsed.BaseURL,
		Model:      parsed.Model,
		SessionDir: parsed.SessionDir,
		CWD:        options.CWD,
	})
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

	sessionStore, loadedMessages, err := openSession(parsed, cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	provider := providerFactory(cfg)
	runner := agent.NewRunner(provider, defaultTools(cfg.CWD, provider, skillSet.ReadRoots()))
	executor := newTurnExecutor(runner, sessionStore, loadedMessages, skillSet)

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
	if stream {
		onDelta = func(text string) {
			fmt.Fprint(stdout, text)
		}
	}
	result, err := executor.Run(ctx, prompt, onDelta)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if stream {
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
	Content string
	Usage   agent.Usage
}

type turnExecutor struct {
	runner   *agent.Runner
	store    *session.Store
	history  []agent.Message
	skillSet skills.Set
}

func newTurnExecutor(runner *agent.Runner, store *session.Store, history []agent.Message, skillSet skills.Set) *turnExecutor {
	return &turnExecutor{
		runner:   runner,
		store:    store,
		history:  append([]agent.Message(nil), history...),
		skillSet: skillSet,
	}
}

func (e *turnExecutor) Run(ctx context.Context, prompt string, onDelta func(string)) (turnResult, error) {
	preparedPrompt, err := preparePrompt(prompt, e.skillSet)
	if err != nil {
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
	reply, err := e.runner.Run(ctx, messages, onEvent)
	if err != nil {
		return turnResult{}, err
	}
	if err := appendNewMessages(e.store, e.runner.Transcript(), len(systemMessages)+len(e.history)); err != nil {
		return turnResult{}, err
	}
	usage := e.runner.Usage()
	if err := appendUsage(e.store, usage); err != nil {
		return turnResult{}, err
	}
	e.history = stripSystemMessages(e.runner.Transcript())
	return turnResult{Content: reply.Content, Usage: usage}, nil
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
		ModelName:       cfg.Model,
		ShowUsage:       showUsage,
		InitialMessages: displayMessages(executor.history),
		Input:           stdin,
		Output:          stdout,
		Submit: func(ctx context.Context, prompt string, onDelta func(string)) (tui.SubmitResult, error) {
			result, err := executor.Run(ctx, prompt, onDelta)
			return tui.SubmitResult{Content: result.Content, Usage: result.Usage}, err
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

func openSession(args cli.Args, cfg config.Config) (*session.Store, []agent.Message, error) {
	if args.NoSession {
		return nil, nil, nil
	}
	path := args.Session
	switch {
	case args.Command == cli.CommandResume:
		resolved, err := session.FindForCWD(cfg.SessionDir, cfg.CWD, args.ResumeTarget)
		if err != nil {
			return nil, nil, err
		}
		path = resolved
	case args.Continue || args.Last:
		latest, err := session.LatestForCWD(cfg.SessionDir, cfg.CWD)
		if err != nil {
			return nil, nil, err
		}
		path = latest.Path
	}
	if path == "" {
		path = defaultSessionPath(cfg.SessionDir, cfg.CWD)
	}
	store, err := session.NewStore(path, cfg.CWD)
	if err != nil {
		return nil, nil, err
	}
	loaded, err := session.Load(store.Path())
	if err != nil {
		return nil, nil, err
	}
	return store, loaded.Messages, nil
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
