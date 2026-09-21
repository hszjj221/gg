package daemon

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/provider/openai"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/transport/httpapi"
	"github.com/hszjj221/gg/internal/transport/jsonrpc"
	"github.com/hszjj221/gg/internal/transport/stdio"
)

type Options struct {
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	Version         string
	HomeDir         string
	ProviderFactory func(config.Config) agent.Provider
}

func Run(ctx context.Context, argv []string, options Options) int {
	stdin := readerOr(options.Stdin, os.Stdin)
	stdout := writerOr(options.Stdout, os.Stdout)
	stderr := writerOr(options.Stderr, os.Stderr)
	fs := flag.NewFlagSet("ggd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		httpAddress    string
		token          string
		allowRemote    bool
		cwd            string
		apiKey         string
		baseURL        string
		model          string
		sessionDir     string
		noSkills       bool
		noMemory       bool
		noContextFiles bool
		showVersion    bool
	)
	fs.StringVar(&httpAddress, "http", "", "serve HTTP on an address such as 127.0.0.1:8765; otherwise use stdio")
	fs.StringVar(&token, "token", "", "bearer token required by HTTP mode")
	fs.BoolVar(&allowRemote, "allow-remote", false, "allow HTTP to bind to a non-loopback address")
	fs.StringVar(&cwd, "cwd", "", "workspace directory")
	fs.StringVar(&apiKey, "api-key", "", "provider API key")
	fs.StringVar(&baseURL, "base-url", "", "OpenAI-compatible base URL")
	fs.StringVar(&model, "model", "", "model selection as provider:model")
	fs.StringVar(&sessionDir, "session-dir", "", "session storage directory")
	fs.BoolVar(&noSkills, "no-skills", false, "disable skills discovery")
	fs.BoolVar(&noMemory, "no-memory", false, "disable memory")
	fs.BoolVar(&noContextFiles, "no-context-files", false, "disable AGENTS.md discovery")
	fs.BoolVar(&showVersion, "version", false, "show version")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "ggd does not accept positional arguments")
		return 2
	}
	if showVersion {
		version := options.Version
		if version == "" {
			version = "dev"
		}
		fmt.Fprintln(stdout, version)
		return 0
	}

	cfg, err := config.Resolve(config.Options{
		APIKey:         apiKey,
		BaseURL:        baseURL,
		Model:          model,
		SessionDir:     sessionDir,
		CWD:            cwd,
		HomeDir:        options.HomeDir,
		NoMemory:       noMemory,
		NoContextFiles: noContextFiles,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	skillSet := skills.Set{}
	if !noSkills {
		skillSet, err = skills.Load(skills.LoadOptions{CWD: cfg.CWD, HomeDir: options.HomeDir})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	providerFactory := options.ProviderFactory
	if providerFactory == nil {
		providerFactory = func(cfg config.Config) agent.Provider {
			return openai.NewClient(openai.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Model: cfg.Model})
		}
	}
	workspace, err := app.NewWorkspace(app.WorkspaceOptions{
		Config:          cfg,
		ProviderFactory: providerFactory,
		Skills:          skillSet,
		Repository:      session.NewFileRepository(cfg.SessionDir),
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	rpc := jsonrpc.NewHandlerWithContext(ctx, workspace)
	if httpAddress == "" {
		if err := stdio.NewServer(rpc, stdin, stdout).Serve(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if token == "" {
		fmt.Fprintln(stderr, "--token is required in HTTP mode")
		return 2
	}
	if !allowRemote && !isLoopbackAddress(httpAddress) {
		fmt.Fprintln(stderr, "HTTP address must be loopback unless --allow-remote is set")
		return 2
	}
	listener, err := net.Listen("tcp", httpAddress)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	server := &http.Server{
		Handler:           httpapi.NewHandler(rpc, workspace, token),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	fmt.Fprintf(stderr, "ggd listening on http://%s\n", listener.Addr())
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func readerOr(value io.Reader, fallback io.Reader) io.Reader {
	if value != nil {
		return value
	}
	return fallback
}

func writerOr(value io.Writer, fallback io.Writer) io.Writer {
	if value != nil {
		return value
	}
	return fallback
}
