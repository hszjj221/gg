package daemon

import (
	"context"
	"io"
	"path/filepath"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/media"
	"github.com/hszjj221/gg/internal/telegram"
)

// newTelegramChannel builds the Telegram bot channel. A construction error
// is fatal to daemon startup.
func newTelegramChannel(cfg config.Config, workspace *app.Workspace, stderr io.Writer) (Channel, error) {
	var mclient *media.Client
	if baseURL := nonEmpty(cfg.MediaBaseURL, cfg.BaseURL); baseURL != "" {
		mclient = media.NewClient(media.Config{
			BaseURL:    baseURL,
			APIKey:     nonEmpty(cfg.MediaAPIKey, cfg.APIKey),
			ImageModel: cfg.MediaImageModel,
			TTSModel:   cfg.MediaTTSModel,
			STTModel:   cfg.MediaSTTModel,
			Dir:        filepath.Join(cfg.HomeDir, ".gg", "media"),
		})
	}
	bot, err := telegram.New(telegram.Config{
		Token:      cfg.TelegramBotToken,
		AllowChats: cfg.TelegramAllowChats,
		Workspace:  workspace,
		Media:      mclient,
		HomeDir:    cfg.HomeDir,
		Stderr:     stderr,
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

func nonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
