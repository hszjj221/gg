package cli

import (
	"bytes"
	"flag"
	"fmt"
	"strings"
)

type Args struct {
	Print      bool
	Help       bool
	Version    bool
	NoSession  bool
	APIKey     string
	BaseURL    string
	Model      string
	Session    string
	SessionDir string
	Prompt     string
}

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
	fs.StringVar(&args.APIKey, "api-key", "", "API key")
	fs.StringVar(&args.BaseURL, "base-url", "", "OpenAI-compatible base URL")
	fs.StringVar(&args.Model, "model", "", "model name")
	fs.StringVar(&args.Session, "session", "", "session JSONL path")
	fs.StringVar(&args.SessionDir, "session-dir", "", "session storage directory")
	if err := fs.Parse(argv); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return Args{}, fmt.Errorf("%s", msg)
	}
	args.Prompt = strings.Join(fs.Args(), " ")
	return args, nil
}

func HelpText() string {
	return strings.TrimSpace(`gg - Go coding agent

Usage:
  gg [options] [prompt]

Options:
  -p, --print              run once and print the final assistant text
  --model <name>           model name (default: GG_MODEL or gpt-4.1)
  --base-url <url>         OpenAI-compatible base URL
  --api-key <key>          API key (default: OPENAI_API_KEY)
  --session <path>         use a specific JSONL session file
  --session-dir <dir>      session directory (default: ~/.gg/sessions)
  --no-session             disable session persistence
  -h, --help               show help
  -v, --version            show version`)
}
