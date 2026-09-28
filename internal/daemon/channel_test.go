package daemon

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/config"
)

type fakeChannel struct {
	name string
	run  func(ctx context.Context) error
}

func (f *fakeChannel) Name() string { return f.name }

func (f *fakeChannel) Run(ctx context.Context) error { return f.run(ctx) }

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestLaunchChannelReportsRuntimeError(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	launchChannel(ctx, &fakeChannel{
		name: "testchan",
		run:  func(ctx context.Context) error { return errors.New("boom") },
	}, &out)

	waitFor(t, "error report", func() bool {
		return strings.Contains(out.String(), "testchan: boom")
	})
	if !strings.Contains(out.String(), "testchan: starting") {
		t.Errorf("expected startup line, got %q", out.String())
	}
}

func TestLaunchChannelSilentOnCleanShutdown(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	launchChannel(ctx, &fakeChannel{
		name: "testchan",
		run: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
	}, &out)
	cancel()
	// Give the goroutine a chance to (incorrectly) report.
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(out.String(), "testchan: testchan") || strings.Count(out.String(), "\n") > 1 {
		t.Errorf("expected only the startup line, got %q", out.String())
	}
}

func TestLaunchChannelIgnoresErrorAfterCancel(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	launchChannel(ctx, &fakeChannel{
		name: "testchan",
		run: func(ctx context.Context) error {
			<-release
			return errors.New("too late")
		},
	}, &out)
	cancel()
	close(release)
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(out.String(), "too late") {
		t.Errorf("error after cancellation must not be reported, got %q", out.String())
	}
}

func TestStartChannelsWithNothingConfigured(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := channelDeps{stderr: &out, noScheduler: true}
	if err := startChannels(ctx, deps); err != nil {
		t.Fatalf("no channels configured: %v", err)
	}
	if out.String() != "" {
		t.Errorf("expected no output, got %q", out.String())
	}
}

func TestNewTelegramChannelRejectsEmptyToken(t *testing.T) {
	var out lockedBuffer
	// Token validation fires before workspace validation in telegram.New.
	if _, err := newTelegramChannel(config.Config{}, nil, &out); err == nil {
		t.Fatal("expected error for empty token")
	}
}
