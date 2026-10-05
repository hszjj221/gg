package daemon

import (
	"io"
	"log/slog"
	"os"

	"github.com/hszjj221/gg/internal/runlog"
)

// newLogger builds the daemon's structured logger: JSON records to stderr.
// The level comes from GG_LOG_LEVEL (debug/info/warn/error, case-insensitive);
// anything unrecognized falls back to info.
func newLogger(stderr io.Writer) *slog.Logger {
	return runlog.NewLogger(stderr, os.Getenv("GG_LOG_LEVEL"))
}

func logLevel(raw string) slog.Level {
	return runlog.Level(raw)
}
