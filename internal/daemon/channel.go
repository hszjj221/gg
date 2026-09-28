package daemon

import (
	"context"
	"fmt"
	"log/slog"

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
	// normal; any other error is reported to the daemon's logger.
	Run(ctx context.Context) error
}

type channelDeps struct {
	cfg         config.Config
	workspace   *app.Workspace
	logger      *slog.Logger
	noScheduler bool
}

// startChannels constructs and launches every configured channel. A
// construction error is fatal — the daemon must not serve half-configured,
// so every channel is constructed before any of them is launched. A channel
// that fails at runtime is reported and left stopped; it does not take the
// daemon down with it.
func startChannels(ctx context.Context, deps channelDeps) error {
	var channels []Channel
	if !deps.noScheduler {
		ch, err := newSchedulerChannel(deps.cfg, deps.workspace, deps.logger)
		if err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
		channels = append(channels, ch)
	}
	if deps.cfg.TelegramBotToken != "" {
		ch, err := newTelegramChannel(deps.cfg, deps.workspace, deps.logger)
		if err != nil {
			return fmt.Errorf("telegram: %w", err)
		}
		channels = append(channels, ch)
	}
	for _, ch := range channels {
		launchChannel(ctx, ch, deps.logger)
	}
	return nil
}

// launchChannel runs ch in the background until ctx is done. Runtime
// failures are reported with the channel name; a nil return on context
// cancellation is silent.
func launchChannel(ctx context.Context, ch Channel, logger *slog.Logger) {
	logger.Info("channel starting", "channel", ch.Name())
	go func() {
		if err := ch.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("channel failed", "channel", ch.Name(), "error", err)
		}
	}()
}
