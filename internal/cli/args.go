package cli

import (
	"bytes"
	"flag"
	"fmt"
	"strings"
)

type Args struct {
	Print          bool
	Help           bool
	Version        bool
	NoSession      bool
	Continue       bool
	Last           bool
	Resume         bool
	Usage          bool
	NoSkills       bool
	NoMemory       bool
	NoContextFiles bool
	Approval       string
	APIKey         string
	BaseURL        string
	Model          string
	Session        string
	SessionDir     string
	Name           string
	Command        Command
	ResumeTarget   string
	Prompt         string
	// KB subcommand fields (gg kb index|search|eval).
	KBSub        string
	KBName       string
	KBPath       string
	KBQuery      string
	KBTopK       int
	KBCases      string
	KBEmbedModel string
	KBEmbedBase  string
	KBEmbedKey   string
	// MemoryArgs carries the subcommand for `gg memory ...`.
	MemoryArgs []string
	// JobArgs carries the subcommand for `gg job ...`.
	JobArgs []string
	// ArtifactArgs carries the subcommand for `gg artifact ...`.
	ArtifactArgs []string
	// LibraryArgs carries the subcommand for `gg library ...`.
	LibraryArgs []string
}

type Command string

const (
	CommandRun          Command = ""
	CommandSessionsList Command = "sessions-list"
	CommandResume       Command = "resume"
	CommandKB           Command = "kb"
	CommandInit         Command = "init"
	CommandMemory       Command = "memory"
	CommandJob          Command = "job"
	CommandArtifact     Command = "artifact"
	CommandLibrary      Command = "library"
)

func Parse(argv []string) (Args, error) {
	var args Args
	fs := flag.NewFlagSet("gg", flag.ContinueOnError)
	var stderr bytes.Buffer
	fs.SetOutput(&stderr)
	fs.BoolVar(&args.Print, "p", false, "print mode")
	fs.BoolVar(&args.Print, "print", false, "print mode")
	fs.BoolVar(&args.Help, "help", false, "show help")
	fs.BoolVar(&args.Help, "h", false, "show help")
	fs.BoolVar(&args.Version, "version", false, "show version")
	fs.BoolVar(&args.Version, "v", false, "show version")
	fs.BoolVar(&args.NoSession, "no-session", false, "disable session persistence")
	fs.BoolVar(&args.Continue, "continue", false, "resume the latest session")
	fs.BoolVar(&args.Last, "last", false, "resume the latest session")
	fs.BoolVar(&args.Resume, "resume", false, "select a session to resume")
	fs.BoolVar(&args.Resume, "r", false, "select a session to resume")
	fs.BoolVar(&args.Usage, "usage", false, "print token usage to stderr")
	fs.BoolVar(&args.NoSkills, "no-skills", false, "disable .agents/skills discovery")
	fs.BoolVar(&args.NoMemory, "no-memory", false, "disable memory (~/.gg/memory/)")
	fs.BoolVar(&args.NoContextFiles, "no-context-files", false, "disable AGENTS.md discovery")
	fs.StringVar(&args.Approval, "approval", "auto", "tool approval mode: auto, never, or on-request")
	fs.StringVar(&args.APIKey, "api-key", "", "API key")
	fs.StringVar(&args.BaseURL, "base-url", "", "OpenAI-compatible base URL")
	fs.StringVar(&args.Model, "model", "", "model selection as provider:model")
	fs.StringVar(&args.Session, "session", "", "session JSONL path")
	fs.StringVar(&args.SessionDir, "session-dir", "", "session storage directory")
	fs.StringVar(&args.Name, "name", "", "session display name")
	fs.StringVar(&args.Name, "n", "", "session display name")
	if err := fs.Parse(argv); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return Args{}, fmt.Errorf("%s", msg)
	}
	if err := parseCommand(&args, fs.Args()); err != nil {
		return Args{}, err
	}
	if err := validateApproval(args.Approval); err != nil {
		return Args{}, err
	}
	return args, nil
}

func validateApproval(mode string) error {
	switch mode {
	case "auto", "never", "on-request":
		return nil
	default:
		return fmt.Errorf("approval must be one of: auto, never, on-request")
	}
}

func parseCommand(args *Args, rest []string) error {
	if len(rest) == 0 {
		return nil
	}
	switch rest[0] {
	case "sessions":
		if len(rest) != 2 || rest[1] != "list" {
			return fmt.Errorf("usage: gg sessions list")
		}
		args.Command = CommandSessionsList
	case "resume":
		args.Command = CommandResume
		if len(rest) >= 2 {
			args.ResumeTarget = rest[1]
			args.Prompt = strings.Join(rest[2:], " ")
		}
	case "kb":
		args.Command = CommandKB
		if err := parseKBCommand(args, rest[1:]); err != nil {
			return err
		}
	case "init":
		// Only a bare `gg init` is a command; anything longer stays a prompt
		// so e.g. `gg init a repo` keeps working as before.
		if len(rest) == 1 {
			args.Command = CommandInit
		} else {
			args.Prompt = strings.Join(rest, " ")
		}
	case "memory":
		// Only `gg memory search|show ...` is a command; other prompts
		// starting with "memory" keep working as before.
		if len(rest) >= 2 && (rest[1] == "search" || rest[1] == "show") {
			if rest[1] == "show" && len(rest) > 3 {
				return fmt.Errorf("usage: gg memory show [daily]")
			}
			args.Command = CommandMemory
			args.MemoryArgs = rest[1:]
		} else {
			args.Prompt = strings.Join(rest, " ")
		}
	case "job":
		// `gg job` alone or with a known subcommand manages scheduled jobs;
		// anything else stays a prompt so `gg job well done` keeps working.
		if len(rest) == 1 || isJobSubcommand(rest[1]) {
			args.Command = CommandJob
			args.JobArgs = rest[1:]
		} else {
			args.Prompt = strings.Join(rest, " ")
		}
	case "artifact":
		// `gg artifact` alone or with a known subcommand manages artifacts;
		// anything else stays a prompt.
		if len(rest) == 1 || isArtifactSubcommand(rest[1]) {
			args.Command = CommandArtifact
			args.ArtifactArgs = rest[1:]
		} else {
			args.Prompt = strings.Join(rest, " ")
		}
	case "library":
		// `gg library` alone or with a known subcommand manages the file
		// library; anything else stays a prompt.
		if len(rest) == 1 || isLibrarySubcommand(rest[1]) {
			args.Command = CommandLibrary
			args.LibraryArgs = rest[1:]
		} else {
			args.Prompt = strings.Join(rest, " ")
		}
	default:
		args.Prompt = strings.Join(rest, " ")
	}
	return nil
}

// isJobSubcommand reports whether word is a `gg job` subcommand.
func isJobSubcommand(word string) bool {
	switch word {
	case "add", "list", "show", "pause", "resume", "remove", "log", "run":
		return true
	}
	return false
}

// isArtifactSubcommand reports whether word is a `gg artifact` subcommand.
func isArtifactSubcommand(word string) bool {
	switch word {
	case "list", "show", "publish", "remove":
		return true
	}
	return false
}

// isLibrarySubcommand reports whether word is a `gg library` subcommand.
func isLibrarySubcommand(word string) bool {
	switch word {
	case "add", "list", "remove", "path":
		return true
	}
	return false
}

// parseKBCommand parses `gg kb <sub> [options]` with its own flag set so
// kb-specific options don't pollute the global flags.
func parseKBCommand(args *Args, rest []string) error {
	if len(rest) == 0 {
		return fmt.Errorf("usage: gg kb <index|search|eval> [options]")
	}
	fs := flag.NewFlagSet("gg kb", flag.ContinueOnError)
	var stderr bytes.Buffer
	fs.SetOutput(&stderr)
	fs.StringVar(&args.KBName, "name", "", "knowledge base name (default: default)")
	fs.IntVar(&args.KBTopK, "top-k", 5, "results per query")
	fs.StringVar(&args.KBCases, "cases", "", "eval cases file (JSONL)")
	fs.StringVar(&args.KBEmbedModel, "embed-model", "", "embedding model (default: text-embedding-3-small)")
	fs.StringVar(&args.KBEmbedBase, "embed-base-url", "", "embeddings base URL (default: GG_EMBED_BASE_URL or --base-url)")
	fs.StringVar(&args.KBEmbedKey, "embed-api-key", "", "embeddings API key (default: GG_EMBED_API_KEY or --api-key)")
	// Go's flag package stops at the first positional argument, so
	// `gg kb index ./docs --name api` would misparse. Reorder first.
	flagArgs, positional := splitKBArgs(rest[1:])
	if err := fs.Parse(flagArgs); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	switch rest[0] {
	case "index":
		args.KBSub = "index"
		if len(positional) != 1 {
			return fmt.Errorf("usage: gg kb index <dir> [options]")
		}
		args.KBPath = positional[0]
	case "search":
		args.KBSub = "search"
		if len(positional) == 0 {
			return fmt.Errorf("usage: gg kb search <query> [options]")
		}
		args.KBQuery = strings.Join(positional, " ")
	case "eval":
		args.KBSub = "eval"
		if args.KBCases == "" {
			return fmt.Errorf("usage: gg kb eval --cases <file> [options]")
		}
		args.KBPath = args.KBCases
	default:
		return fmt.Errorf("unknown kb subcommand %q: want index, search, or eval", rest[0])
	}
	return nil
}

// splitKBArgs partitions argv into flag tokens and positional tokens so
// flags may appear before or after positional arguments. A `--` token ends
// flag parsing; everything after it is positional.
func splitKBArgs(argv []string) (flags, positional []string) {
	i := 0
	for i < len(argv) {
		a := argv[i]
		if a == "--" {
			positional = append(positional, argv[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				i++
				flags = append(flags, argv[i])
			}
		} else {
			positional = append(positional, a)
		}
		i++
	}
	return flags, positional
}

func HelpText() string {
	return strings.TrimSpace(`gg - Go coding agent

Usage:
  gg
  gg [options] [prompt]
  gg sessions list
  gg resume [<id-or-path> [prompt]]
  gg kb index <dir> [--name <kb>] [embedding options]
  gg kb search <query> [--name <kb>] [--top-k <n>]
  gg kb eval --cases <file> [--name <kb>] [--top-k <n>]
  gg init
  gg memory search <query>
  gg memory show [daily]
  gg job add --cron "0 9 * * *" --name NAME PROMPT
  gg job list [--all]
  gg artifact list
  gg artifact show <id>
  gg artifact publish <id>
  gg library add <path> [--name NAME]
  gg library list

Running gg without a prompt starts the TUI interactive mode when stdin/stdout are terminals.

Options:
  -p, --print              run once and print the final assistant text
  --model <provider:model> model selection (default: ~/.gg/config.json or openai:gpt-4.1)
  --base-url <url>         OpenAI-compatible base URL
  --api-key <key>          API key (default: OPENAI_API_KEY)
  --session <path>         use a specific JSONL session file
  --session-dir <dir>      session directory (default: ~/.gg/sessions)
  --no-session             disable session persistence
  --continue               resume the latest session
  --last                   resume the latest session
  -r, --resume             select a session to resume
  -n, --name <name>        set the session display name
  --usage                  print token usage to stderr
  --no-skills              disable .agents/skills discovery
  --no-memory              disable memory (~/.gg/memory/)
  --no-context-files       disable AGENTS.md discovery
  --approval <mode>        tool approval mode: auto, never, on-request (default: auto)
  -h, --help               show help
  -v, --version            show version

Knowledge base (RAG):
  gg kb index <dir>        chunk, embed, and index text files under <dir>
  gg kb search <query>     semantic search over the local index
  gg kb eval --cases <f>   run retrieval eval cases (JSONL: {"query","expect"})
  --name <kb>              knowledge base name (default: default)
  --top-k <n>              results per query (default: 5)
  --embed-model <m>        embedding model (default: text-embedding-3-small)
  --embed-base-url <url>   embeddings base URL (default: GG_EMBED_BASE_URL or --base-url)
  --embed-api-key <key>    embeddings API key (default: GG_EMBED_API_KEY or --api-key)

Scheduled jobs (fire while the ggd daemon is alive):
  gg job add --cron "0 9 * * *" --name NAME [--timezone TZ] [--allow-all] [--timeout 10m] PROMPT
  gg job add --at "2026-09-28 15:04" --name NAME PROMPT
  gg job add --in 20m --name NAME PROMPT
  gg job list [--all]      list jobs (paused hidden unless --all)
  gg job show <id|name>    show one job
  gg job pause|resume|remove <id|name>
  gg job log [--job <id|name>] [--limit N]
  gg job run <id|name>     run once now without changing the schedule

Artifacts (agent-produced deliverables, ~/.gg/artifacts):
  gg artifact list          list artifacts
  gg artifact show <id>     show metadata and latest version content
  gg artifact publish <id>  publish latest version and save it to the library
  gg artifact remove <id>   delete an artifact and all its versions

Library (your file collection, ~/.gg/library):
  gg library add <path> [--name NAME]   copy a file into the library
  gg library list          list library files
  gg library remove <id|name>
  gg library path <id|name>  print the stored file path`)
}
