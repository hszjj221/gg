package daemon

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// newLogger builds the daemon's structured logger: JSON records to stderr.
// The level comes from GG_LOG_LEVEL (debug/info/warn/error, case-insensitive);
// anything unrecognized falls back to info.
func newLogger(stderr io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{
		Level: logLevel(os.Getenv("GG_LOG_LEVEL")),
	}))
}

func logLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
