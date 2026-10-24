package daemon

import (
	"context"
	"log/slog"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/media"
	"github.com/hszjj221/gg/internal/telegram"
)

// newTelegramChannel is the statically linked Telegram registration adapter.
// It maps host config to telegram.New without starting the bot. Core's
// startChannels/launchChannel own startup, supervision, and cancellation;
// a construction error remains fatal before any channel launches.
func newTelegramChannel(cfg config.Config, rt *app.Runtime, logger *slog.Logger) (Channel, error) {
	var mclient *media.Client
	if mc, ok := cfg.MediaClientConfig(); ok {
		mclient = media.NewClient(mc)
	}
	bot, err := telegram.New(telegram.Config{
		Token:      cfg.TelegramBotToken,
		AllowChats: cfg.TelegramAllowChats,
		Runtime:    rt,
		Media:      mclient,
		HomeDir:    cfg.HomeDir,
		Logger:     logger,
	})
	if err != nil {
		return nil, err
	}
	return &telegramChannel{bot: bot}, nil
}

type telegramChannel struct {
	bot *telegram.Bot
}

func (c *telegramChannel) Name() string { return "telegram" }

func (c *telegramChannel) Run(ctx context.Context) error { return c.bot.Run(ctx) }
