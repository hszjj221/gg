package daemon

import (
	"context"
	"fmt"
	"io"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
)

// Channel is a long-running daemon background service: the job scheduler,
// a messaging bot, and any future channel. Every channel shares one
// startup and error-reporting path so a new channel cannot invent its own.
type Channel interface {
	// Name identifies the channel in logs and error messages.
	Name() string
	// Run serves until ctx is done. Returning nil on cancellation is
	// normal; any other error is reported to the daemon's stderr.
	Run(ctx context.Context) error
}

type channelDeps struct {
	cfg         config.Config
	workspace   *app.Workspace
	stderr      io.Writer
	noScheduler bool
}

// startChannels constructs and launches every configured channel. A
// construction error is fatal — the daemon must not serve half-configured.
// A channel that fails at runtime is reported and left stopped; it does
// not take the daemon down with it.
func startChannels(ctx context.Context, deps channelDeps) error {
	if !deps.noScheduler {
		ch, err := newSchedulerChannel(deps.cfg, deps.workspace, deps.stderr)
		if err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
		launchChannel(ctx, ch, deps.stderr)
	}
	if deps.cfg.TelegramBotToken != "" {
		ch, err := newTelegramChannel(deps.cfg, deps.workspace, deps.stderr)
		if err != nil {
			return fmt.Errorf("telegram: %w", err)
		}
		launchChannel(ctx, ch, deps.stderr)
	}
	return nil
}

// launchChannel runs ch in the background until ctx is done. Runtime
// failures are reported with the channel name; a nil return on context
// cancellation is silent.
func launchChannel(ctx context.Context, ch Channel, stderr io.Writer) {
	fmt.Fprintf(stderr, "%s: starting\n", ch.Name())
	go func() {
		if err := ch.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(stderr, "%s: %s\n", ch.Name(), err)
		}
	}()
}
