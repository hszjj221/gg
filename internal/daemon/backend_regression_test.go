package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMonitorWaitDrainsChannels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mon := NewMonitor()
	log := testStdinLogger()
	launchChannel(ctx, &fakeChannel{name: "wait", run: func(ctx context.Context) error { <-ctx.Done(); return nil }}, log, mon)
	cancel()
	deadline, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := mon.Wait(deadline); err != nil {
		t.Fatal(err)
	}
	if states := mon.Snapshot(); len(states) != 1 || states[0].State != ChannelStopped {
		t.Fatalf("channel not drained: %v", states)
	}
}
func TestRunConstructionFailureCleansUpRuntime(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gg"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gg", "telegram"), []byte("blocked directory"), 0600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	t.Setenv("GG_TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("GG_TELEGRAM_ALLOW_CHATS", "")
	// A regular file where the Telegram directory belongs forces a local
	// construction failure before any channel or network request starts.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code := Run(ctx, []string{"--http", "127.0.0.1:0", "--token", "test", "--no-scheduler"}, testRunOptions(home, &stderr))
	if code != 1 || !strings.Contains(stderr.String(), "start channels") {
		t.Fatalf("startup failure: code=%d output=%s", code, stderr.String())
	}
}
