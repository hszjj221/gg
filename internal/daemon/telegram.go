package daemon

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/media"
	"github.com/hszjj221/gg/internal/telegram"
)

// startTelegram launches the Telegram bot channel in the background when
// GG_TELEGRAM_BOT_TOKEN is set. It returns nil immediately; the bot runs
// until ctx is done.
func startTelegram(ctx context.Context, cfg config.Config, workspace *app.Workspace, stderr io.Writer) error {
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
	})
	if err != nil {
		return err
	}
	go func() {
		if err := bot.Run(ctx); err != nil {
			fmt.Fprintln(stderr, "telegram: "+err.Error())
		}
	}()
	return nil
}

func nonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
