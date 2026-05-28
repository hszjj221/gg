package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/provider/openai"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/tools"
)

type Options struct {
	CWD             string
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	Version         string
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

	toolset := []agent.Tool{
		tools.NewReadTool(cfg.CWD),
		tools.NewBashTool(cfg.CWD, tools.BashOptions{}),
		tools.NewEditTool(cfg.CWD),
		tools.NewWriteTool(cfg.CWD),
	}
	runner := agent.NewRunner(providerFactory(cfg), toolset)

	if parsed.Prompt != "" {
		return runPrompt(ctx, runner, sessionStore, loadedMessages, parsed.Prompt, stdout, stderr, false)
	}
	if parsed.Print {
		fmt.Fprintln(stderr, "prompt is required in print mode")
		return 2
	}
	return runInteractive(ctx, runner, sessionStore, loadedMessages, stdin, stdout, stderr)
}

func runPrompt(
	ctx context.Context,
	runner *agent.Runner,
	store *session.Store,
	history []agent.Message,
	prompt string,
	stdout io.Writer,
	stderr io.Writer,
	stream bool,
) int {
	user := agent.Message{Role: agent.RoleUser, Content: prompt, Timestamp: time.Now().UnixMilli()}
	messages := append(append([]agent.Message(nil), history...), user)
	var onEvent func(agent.Event)
	if stream {
		onEvent = func(event agent.Event) {
			if event.Type == agent.EventTextDelta {
				fmt.Fprint(stdout, event.Text)
			}
		}
	}
	reply, err := runner.Run(ctx, messages, onEvent)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if stream {
		fmt.Fprintln(stdout)
	} else {
		fmt.Fprintln(stdout, reply.Content)
	}
	if err := appendNewMessages(store, runner.Transcript(), len(history)); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runInteractive(
	ctx context.Context,
	runner *agent.Runner,
	store *session.Store,
	history []agent.Message,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
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
		code := runPrompt(ctx, runner, store, history, prompt, stdout, stderr, true)
		if code != 0 {
			return code
		}
		history = runner.Transcript()
	}
	if err := scanner.Err(); err != nil {
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

func openSession(args cli.Args, cfg config.Config) (*session.Store, []agent.Message, error) {
	if args.NoSession {
		return nil, nil, nil
	}
	path := args.Session
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
	name := sanitizePath(cwd)
	filename := fmt.Sprintf("%d.jsonl", time.Now().UnixNano())
	return filepath.Join(sessionDir, name, filename)
}

func sanitizePath(path string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	safe := strings.Trim(replacer.Replace(path), "-")
	if safe == "" {
		return "default"
	}
	return safe
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
