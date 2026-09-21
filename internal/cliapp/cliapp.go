package cliapp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/provider/openai"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
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
		APIKey:         parsed.APIKey,
		BaseURL:        parsed.BaseURL,
		Model:          parsed.Model,
		SessionDir:     parsed.SessionDir,
		CWD:            options.CWD,
		HomeDir:        options.HomeDir,
		NoMemory:       parsed.NoMemory,
		NoContextFiles: parsed.NoContextFiles,
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
	if wantsSessionSelector(parsed) {
		if !shouldRunTUI(stdin, stdout, isTerm) {
			fmt.Fprintln(stderr, "session selector requires a terminal; use gg resume <id-or-path>")
			return 2
		}
		selected, ok, err := selectSession(ctx, cfg, stdin, stdout)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !ok {
			return 0
		}
		parsed.Command = cli.CommandResume
		parsed.ResumeTarget = selected
		parsed.Resume = false
	}
	if parsed.Approval == "on-request" && !approvalTerminalAvailable(parsed, stdin, stdout, stderr, isTerm) {
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
	if parsed.Name != "" && (loaded.LastInfo == nil || loaded.LastInfo.Name != parsed.Name) {
		if err := sessionStore.AppendName(parsed.Name); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		loaded.LastInfo = &session.SessionInfoEntry{Name: parsed.Name}
	}
	if parsed.Model == "" && loaded.LastModel != nil {
		cfg, err = cfg.WithSelection(loaded.LastModel.Selection)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	modelRecorded := loaded.LastModel != nil && loaded.LastModel.Selection == cfg.Selection
	executor := app.NewService(app.Options{
		Config:          cfg,
		ProviderFactory: providerFactory,
		Store:           sessionStore,
		History:         loaded.Messages,
		Summary:         loaded.LastSummary,
		Skills:          skillSet,
		ModelRecorded:   modelRecorded,
	})

	if parsed.Prompt != "" {
		return runPrompt(ctx, executor, parsed.Prompt, stdout, stderr, false, parsed.Usage, promptApprover(parsed, stdin, stderr))
	}
	if parsed.Print {
		fmt.Fprintln(stderr, "prompt is required in print mode")
		return 2
	}
	if shouldRunTUI(stdin, stdout, isTerm) {
		return runTUI(ctx, executor, cfg, sessionName(loaded), stdin, stdout, stderr, parsed.Usage, parsed.Approval != "never")
	}
	reader := bufio.NewReader(stdin)
	return runInteractive(ctx, executor, reader, stdout, stderr, sessionName(loaded), parsed.Usage, interactiveApprover(parsed, reader, stderr, bothTerminals(stdin, stdout, isTerm)))
}

func runPrompt(
	ctx context.Context,
	executor *app.Service,
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

func validateSessionArgs(args cli.Args) error {
	resume := args.Command == cli.CommandResume || args.Resume
	if args.NoSession && (resume || args.Continue || args.Last || args.Name != "") {
		return fmt.Errorf("--no-session cannot be used with resume, --continue, --last, or --name")
	}
	if args.Session != "" && (resume || args.Continue || args.Last) {
		return fmt.Errorf("--session cannot be combined with resume, --continue, or --last")
	}
	if resume && (args.Continue || args.Last) {
		return fmt.Errorf("resume cannot be combined with --continue or --last")
	}
	return nil
}

func wantsSessionSelector(args cli.Args) bool {
	return args.Resume || (args.Command == cli.CommandResume && args.ResumeTarget == "")
}

func selectSession(ctx context.Context, cfg config.Config, stdin io.Reader, stdout io.Writer) (string, bool, error) {
	infos, err := session.ListForCWD(cfg.SessionDir, cfg.CWD)
	if err != nil {
		return "", false, err
	}
	if len(infos) == 0 {
		return "", false, fmt.Errorf("no sessions found for %s", cfg.CWD)
	}
	items := make([]tui.SessionItem, 0, len(infos))
	for _, info := range infos {
		items = append(items, tui.SessionItem{
			ID:           info.ID,
			Path:         info.Path,
			Name:         info.Name,
			Timestamp:    info.Timestamp,
			MessageCount: info.MessageCount,
			Preview:      info.Preview,
		})
	}
	return tui.RunSessionSelector(ctx, items, stdin, stdout)
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
	fmt.Fprintln(w, "ID\tUPDATED\tMESSAGES\tNAME\tPATH\tPREVIEW")
	for _, info := range infos {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", info.ID, info.Timestamp, info.MessageCount, info.Name, info.Path, info.Preview)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runInteractive(
	ctx context.Context,
	executor *app.Service,
	stdin *bufio.Reader,
	stdout io.Writer,
	stderr io.Writer,
	initialSessionName string,
	showUsage bool,
	approver agent.Approver,
) int {
	fmt.Fprintln(stdout, "gg interactive mode. Press Ctrl+D to exit.")
	currentSessionName := initialSessionName
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
		if handled, commandErr := handleSessionCommand(prompt, executor, &currentSessionName, stdout); handled {
			if commandErr != nil {
				fmt.Fprintln(stderr, commandErr)
			}
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

func handleSessionCommand(prompt string, service *app.Service, name *string, stdout io.Writer) (bool, error) {
	if prompt == "/name" {
		if *name == "" {
			fmt.Fprintln(stdout, "session is unnamed; use /name <name>")
		} else {
			fmt.Fprintln(stdout, *name)
		}
		return true, nil
	}
	if !strings.HasPrefix(prompt, "/name ") {
		return false, nil
	}
	next := strings.TrimSpace(strings.TrimPrefix(prompt, "/name "))
	if next == "--clear" {
		next = ""
	}
	if err := service.RenameSession(next); err != nil {
		return true, err
	}
	*name = next
	if next == "" {
		fmt.Fprintln(stdout, "session name cleared")
	} else {
		fmt.Fprintf(stdout, "session named: %s\n", next)
	}
	return true, nil
}

func runTUI(
	ctx context.Context,
	executor *app.Service,
	cfg config.Config,
	sessionName string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	showUsage bool,
	enableApproval bool,
) int {
	snapshot := executor.Snapshot()
	var sessionAction tui.SessionActionFunc
	if executor.HasSession() {
		sessionAction = executor.HandleSessionAction
	}
	err := tui.Run(ctx, tui.Config{
		Queue:           executor.Queue(),
		CWD:             cfg.CWD,
		ModelName:       cfg.Selection,
		SessionName:     sessionName,
		ShowUsage:       showUsage,
		EnableApproval:  enableApproval,
		InitialMessages: displayMessages(snapshot.Messages),
		TreeItems:       snapshot.TreeItems,
		Input:           stdin,
		Output:          stdout,
		Submit:          executor.Run,
		RenameSession:   executor.RenameSession,
		SessionAction:   sessionAction,
	})
	if err != nil && err != context.Canceled {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func sessionName(loaded session.Loaded) string {
	if loaded.LastInfo == nil {
		return ""
	}
	return loaded.LastInfo.Name
}

func loadSkills(args cli.Args, cwd, homeDir string) (skills.Set, error) {
	if args.NoSkills {
		return skills.Set{}, nil
	}
	return skills.Load(skills.LoadOptions{CWD: cwd, HomeDir: homeDir})
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

func approvalTerminalAvailable(args cli.Args, stdin io.Reader, stdout io.Writer, stderr io.Writer, isTerm func(any) bool) bool {
	if args.Prompt == "" && !args.Print && shouldRunTUI(stdin, stdout, isTerm) {
		return true
	}
	return isTerm(stdin) && isTerm(stderr)
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
	repository := session.NewFileRepository(cfg.SessionDir)
	switch {
	case args.Command == cli.CommandResume:
		return repository.OpenForCWD(cfg.CWD, args.ResumeTarget, true)
	case args.Continue || args.Last:
		return repository.OpenLatest(cfg.CWD)
	case args.Session != "":
		return repository.OpenPath(args.Session, cfg.CWD)
	default:
		return repository.Create(cfg.CWD)
	}
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
