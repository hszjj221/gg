package daemon

import (
	"log/slog"
	"testing"
)

func TestLogLevelParsesKnownValues(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"bogus":   slog.LevelInfo,
	}
	for raw, want := range cases {
		if got := logLevel(raw); got != want {
			t.Errorf("logLevel(%q) = %v, want %v", raw, got, want)
		}
	}
}
