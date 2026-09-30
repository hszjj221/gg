package daemon

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
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
	// The Electron sidecar passes --exit-on-stdin-eof and holds the write end
	// of a control pipe: if the parent dies without cleanup, stdin reaches
	// EOF and the watcher started below cancels this derived context, so the
	// daemon shuts down instead of lingering as an orphan.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Service management subcommands run before flag parsing: they take no
	// daemon flags of their own. install-service bakes everything after an
	// optional "--" separator into the service's start command.
	if len(argv) > 0 {
		home := resolveHome(options.HomeDir)
		switch argv[0] {
		case "install-service":
			args := argv[1:]
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			return installService(stdout, stderr, home, args)
		case "uninstall-service":
			return uninstallService(stdout, stderr, home)
		case "status":
			return daemonStatus(stdout, home)
		}
	}
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
		noScheduler    bool
		showVersion    bool
		exitOnStdinEOF bool
	)
	fs.StringVar(&httpAddress, "http", "", "serve HTTP on an address such as 127.0.0.1:8765; otherwise use stdio")
	fs.StringVar(&token, "token", "", "bearer token required by HTTP mode")
	fs.BoolVar(&allowRemote, "allow-remote", false, "allow HTTP to bind to a non-loopback address")
	fs.BoolVar(&exitOnStdinEOF, "exit-on-stdin-eof", false, "exit when stdin reaches EOF (lets a parent process own the daemon lifetime via a control pipe)")
	fs.StringVar(&cwd, "cwd", "", "workspace directory")
	fs.StringVar(&apiKey, "api-key", "", "provider API key")
	fs.StringVar(&baseURL, "base-url", "", "OpenAI-compatible base URL")
	fs.StringVar(&model, "model", "", "model selection as provider:model")
	fs.StringVar(&sessionDir, "session-dir", "", "session storage directory")
	fs.BoolVar(&noSkills, "no-skills", false, "disable skills discovery")
	fs.BoolVar(&noMemory, "no-memory", false, "disable memory")
	fs.BoolVar(&noContextFiles, "no-context-files", false, "disable AGENTS.md discovery")
	fs.BoolVar(&noScheduler, "no-scheduler", false, "disable the background job scheduler")
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
	personal, notice, err := app.SetupPersonal(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	logger := newLogger(stderr)
	if notice != "" {
		logger.Info("startup notice", "notice", notice)
	}
	workspace, err := app.NewWorkspace(app.WorkspaceOptions{
		Config:          cfg,
		ProviderFactory: providerFactory,
		Skills:          skillSet,
		Repository:      session.NewFileRepository(cfg.SessionDir),
		Profile:         personal.Profile,
		MemoryStore:     personal.Store,
		ArtifactStore:   openArtifactStore(logger, cfg),
		LibraryStore:    openLibraryStore(logger, cfg),
	})
	if err != nil {
		logger.Error("open workspace", "error", err)
		return 1
	}
	rpc := jsonrpc.NewHandlerWithContext(ctx, workspace)
	cleanupPid, err := WritePidFile(cfg.HomeDir)
	if err != nil {
		logger.Warn("pid file", "error", err)
	} else {
		defer cleanupPid()
	}
	if err := startChannels(ctx, channelDeps{
		cfg:         cfg,
		workspace:   workspace,
		logger:      logger,
		noScheduler: noScheduler,
	}); err != nil {
		logger.Error("start channels", "error", err)
		return 1
	}
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
	if exitOnStdinEOF {
		go watchStdinEOF(stdin, logger, cancel)
	}
	logger.Info("http listening", "addr", listener.Addr().String())
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("http shutdown", "error", err)
			return 1
		}
		return 0
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("http serve", "error", err)
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

// watchStdinEOF drains stdin until EOF (or a read error) and then cancels the
// daemon context so the HTTP server shuts down gracefully. A supervising
// parent holds the write end of a control pipe and never writes: when the
// parent exits without cleanup — a crash or SIGKILL skips Electron's
// before-quit hook — the pipe closes, stdin reaches EOF, and the sidecar
// terminates instead of lingering with a lost token and port.
func watchStdinEOF(stdin io.Reader, logger *slog.Logger, cancel context.CancelFunc) {
	defer cancel()
	buffer := make([]byte, 512)
	for {
		if _, err := stdin.Read(buffer); err != nil {
			if err == io.EOF {
				logger.Info("stdin EOF: shutting down")
			} else {
				logger.Error("stdin watcher", "error", err)
			}
			return
		}
	}
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

// openArtifactStore opens the artifact store, warning and degrading to nil
// (methods then report "not available") instead of failing daemon startup.
func openArtifactStore(logger *slog.Logger, cfg config.Config) *artifact.Store {
	store, err := artifact.Open(cfg.Artifacts.Dir)
	if err != nil {
		logger.Warn("artifact store unavailable", "error", err)
		return nil
	}
	return store
}

// openLibraryStore opens the library store with the same degrade-to-nil
// policy as the artifact store.
func openLibraryStore(logger *slog.Logger, cfg config.Config) *library.Store {
	store, err := library.Open(cfg.Library.Dir)
	if err != nil {
		logger.Warn("library store unavailable", "error", err)
		return nil
	}
	return store
}
