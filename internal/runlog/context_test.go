package runlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestOutcome(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, "success"},
		{"canceled", context.Canceled, "canceled"},
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"wrapped canceled", fmt.Errorf("run: %w", context.Canceled), "canceled"},
		{"wrapped timeout", fmt.Errorf("run: %w", context.DeadlineExceeded), "timeout"},
		{"failed", errors.New("failed"), "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Outcome(tc.err); got != tc.want {
				t.Errorf("Outcome(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestLogger(t *testing.T) {
	injected := NewLogger(io.Discard, "debug")
	fallback := NewLogger(io.Discard, "info")
	ctx := context.Background()
	if got := Logger(ctx, fallback); got != fallback {
		t.Errorf("Logger without injection = %p, want fallback %p", got, fallback)
	}
	if got := Logger(WithLogger(ctx, injected), fallback); got != injected {
		t.Errorf("Logger with injection = %p, want injected %p", got, injected)
	}
}
