package runlog

import (
	"log/slog"
	"testing"
)

func TestLevel(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{" DEBUG \t", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{" WaRn\n", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"\tWARNING ", slog.LevelWarn},
		{"error", slog.LevelError},
		{" Error ", slog.LevelError},
		{"info", slog.LevelInfo},
		{" INFO ", slog.LevelInfo},
		{"", slog.LevelInfo},
		{" \t\n", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if got := Level(tc.raw); got != tc.want {
				t.Errorf("Level(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
