package cliapp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/provider"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/userprofile"
	"github.com/hszjj221/gg/internal/workspace"
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
	// NoSkills mirrors cli.Args.NoSkills for subcommands that load skills
	// themselves (e.g. `gg job run`).
	NoSkills bool
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
	// P1 workspace: the process working directory is auto-registered as the
	// "default" workspace on first run after upgrade. Today the runtime
	// still serves this single root exactly like before; later phases bind
	// sessions to workspaces.
	if _, _, err := workspace.EnsureDefaultWorkspace(cfg.HomeDir, cfg.CWD); err != nil {
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
	if parsed.Command == cli.CommandKB {
		return runKB(ctx, cfg, parsed, stdout, stderr)
	}
	if parsed.Command == cli.CommandInit {
		return runInit(cfg, stdout, stderr)
	}
	if parsed.Command == cli.CommandMemory {
		return runMemoryCommand(cfg, parsed.MemoryArgs, stdout, stderr)
	}
	if parsed.Command == cli.CommandJob {
		options.NoSkills = parsed.NoSkills
		return runJobCommand(ctx, cfg, options, parsed.JobArgs, stdout, stderr)
	}
	if parsed.Command == cli.CommandArtifact {
		return runArtifactCommand(cfg, parsed.ArtifactArgs, stdout, stderr)
	}
	if parsed.Command == cli.CommandLibrary {
		return runLibraryCommand(cfg, parsed.LibraryArgs, stdout, stderr)
	}
	if parsed.Command == cli.CommandConnect {
		return runConnectCommand(ctx, cfg, parsed.ConnectArgs, stdout, stderr)
	}
	if parsed.Command == cli.CommandMedia {
		return runMediaCommand(ctx, cfg, parsed.MediaArgs, stdout, stderr)
	}
	if parsed.Command == cli.CommandBrowser {
		return runBrowserCommand(ctx, parsed.BrowserArgs, stdout, stderr)
	}
	personal, notice, err := app.SetupPersonal(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if notice != "" {
		fmt.Fprintln(stderr, "note: "+notice)
	}
	if wantsSessionSelector(parsed) {
		if !bothTerminals(stdin, stdout, isTerm) {
			fmt.Fprintln(stderr, "session selector requires a terminal; use gg resume <id-or-path>")
			return 2
		}
		selected, ok, err := selectSession(cfg, stdin, stdout)
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
		providerFactory = provider.New
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
	executor, err := app.NewSessionService(app.Options{
		Config:          cfg,
		ProviderFactory: providerFactory,
		Skills:          skillSet,
		Profile:         personal.Profile,
		MemoryStore:     workspaceMemoryStore(cfg, personal.Store),
	}, sessionStore, loaded, parsed.Model)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() {
		if err := executor.Close(); err != nil {
			fmt.Fprintln(stderr, "close conversation:", err)
		}
	}()

	if parsed.Prompt != "" {
		return runPrompt(ctx, executor, parsed.Prompt, stdout, stderr, false, parsed.Usage, promptApprover(parsed, stdin, stderr), isTerm)
	}
	if parsed.Print {
		fmt.Fprintln(stderr, "prompt is required in print mode")
		return 2
	}
	reader := bufio.NewReader(stdin)
	// Line-based approval always runs through the stderr prompt, which needs
	// stdin + stderr as terminals (see lineApprovalTerminals).
	return runInteractive(ctx, executor, reader, stdout, stderr, sessionName(loaded), parsed.Usage, interactiveApprover(parsed, reader, stderr, lineApprovalTerminals(stdin, stderr, isTerm)), isTerm)
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
	isTerm func(any) bool,
) int {
	var onDelta func(string)
	var streamed strings.Builder
	stderrTerm := isTerm != nil && isTerm(stderr)
	if stream {
		onDelta = func(text string) {
			streamed.WriteString(text)
			fmt.Fprint(stdout, text)
		}
	}
	onEvent := func(event agent.Event) {
		switch event.Type {
		case agent.EventTextDelta:
			if onDelta != nil {
				onDelta(event.Text)
			}
		case agent.EventThinkingDelta:
			// Thinking streams to stderr so scripted stdout stays clean,
			// in both streaming and one-shot modes.
			text := event.Text
			if stderrTerm {
				text = "\x1b[2m" + text + "\x1b[0m"
			}
			fmt.Fprint(stderr, text)
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

func selectSession(cfg config.Config, stdin io.Reader, stdout io.Writer) (string, bool, error) {
	infos, err := session.ListForCWD(cfg.SessionDir, cfg.CWD)
	if err != nil {
		return "", false, err
	}
	if len(infos) == 0 {
		return "", false, fmt.Errorf("no sessions found for %s", cfg.CWD)
	}
	for i, info := range infos {
		name := info.Name
		if name == "" {
			name = "-"
		}
		fmt.Fprintf(stdout, "[%d] %s  %s  %d msgs  %s\n", i+1, info.ID, info.Timestamp, info.MessageCount, name)
		if info.Preview != "" {
			fmt.Fprintf(stdout, "    %s\n", info.Preview)
		}
	}
	fmt.Fprint(stdout, "Select session [1-", len(infos), "] (empty to cancel): ")
	reader := bufio.NewReader(stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", false, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(infos) {
		return "", false, fmt.Errorf("invalid selection %q", line)
	}
	return infos[n-1].ID, true, nil
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

// runInit scaffolds the personal layer: the ~/.gg/USER.md profile template
// and the ~/.gg/memory/ directory layout. It is idempotent.
func runInit(cfg config.Config, stdout io.Writer, stderr io.Writer) int {
	personal, notice, err := app.SetupPersonal(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_ = personal
	if err := userprofile.WriteTemplate(cfg.UserFile); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "initialized personal layer:\n  profile: %s\n  memory:  %s\n", cfg.UserFile, cfg.Memory.Dir)
	if notice != "" {
		fmt.Fprintf(stdout, "  note: %s\n", notice)
	}
	fmt.Fprintf(stdout, "edit %s to tell gg about yourself.\n", cfg.UserFile)
	return 0
}

// workspaceMemoryStore returns the memory store for a CLI invocation in
// cfg.CWD: the workspace overlay when the CWD belongs to a registered
// workspace, otherwise the global store. The startup path already
// auto-registers the CWD (EnsureDefaultWorkspace), so the lookup normally
// hits. Without this, the agent tools' workspace layer (and memory_add's
// default target=workspace) would silently operate on global memory,
// leaking project-specific facts across workspaces.
func workspaceMemoryStore(cfg config.Config, global *memory.Store) memory.StoreAPI {
	reg, err := workspace.Load(cfg.HomeDir)
	if err != nil {
		return global
	}
	ws, ok := reg.FindByRoot(cfg.CWD)
	if !ok {
		return global
	}
	return memory.NewOverlay(memory.OverlayDir(ws.Root), global)
}

// runMemoryCommand implements `gg memory search <query>` and
// `gg memory show [daily]`.
func runMemoryCommand(cfg config.Config, memArgs []string, stdout io.Writer, stderr io.Writer) int {
	if !cfg.Memory.Enabled {
		fmt.Fprintln(stderr, "memory is disabled")
		return 1
	}
	personal, _, err := app.SetupPersonal(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store := workspaceMemoryStore(cfg, personal.Store)
	if len(memArgs) == 0 {
		fmt.Fprintln(stderr, "usage: gg memory <search <query>|show [daily]>")
		return 2
	}
	switch memArgs[0] {
	case "search":
		query := strings.Join(memArgs[1:], " ")
		if strings.TrimSpace(query) == "" {
			fmt.Fprintln(stderr, "usage: gg memory search <query>")
			return 2
		}
		hits, err := store.SearchLayered(query, "all")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(hits) == 0 {
			fmt.Fprintln(stdout, "no memory matches")
			return 0
		}
		for _, hit := range hits {
			fmt.Fprintf(stdout, "[%s] %s:%d: %s\n", hit.Layer, hit.Path, hit.Line, hit.Snippet)
		}
		return 0
	case "show":
		var content string
		if len(memArgs) > 1 && memArgs[1] == "daily" {
			content, err = store.ShowDaily()
		} else {
			content, err = store.ShowCurated()
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, content)
		return 0
	default:
		fmt.Fprintln(stderr, "usage: gg memory <search <query>|show [daily]>")
		return 2
	}
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
	isTerm func(any) bool,
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
		code := runPrompt(ctx, executor, prompt, stdout, stderr, true, showUsage, approver, isTerm)
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

func bothTerminals(stdin io.Reader, stdout io.Writer, isTerm func(any) bool) bool {
	return isTerm(stdin) && isTerm(stdout)
}

// lineApprovalTerminals reports whether the line-based approval prompter can
// interact with the user. The prompter writes the whole question to stderr
// and then blocks reading the answer from stdin, so both must be terminals.
// Gating on stdout alone is not enough: with stderr redirected the prompt
// would be invisible while the CLI appears hung waiting for input.
func lineApprovalTerminals(stdin io.Reader, stderr io.Writer, isTerm func(any) bool) bool {
	return isTerm(stdin) && isTerm(stderr)
}

func approvalTerminalAvailable(args cli.Args, stdin io.Reader, stdout io.Writer, stderr io.Writer, isTerm func(any) bool) bool {
	if args.Prompt == "" && !args.Print {
		return bothTerminals(stdin, stdout, isTerm) && isTerm(stderr)
	}
	return lineApprovalTerminals(stdin, stderr, isTerm)
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
