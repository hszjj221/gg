package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"

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

// testLogger returns a JSON logger writing to out for structured assertions.
func testLogger(out *lockedBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, nil))
}

// logRecords decodes each JSON log line into a field map.
func logRecords(t *testing.T, out *lockedBuffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func TestLaunchChannelReportsRuntimeError(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	launchChannel(ctx, &fakeChannel{
		name: "testchan",
		run:  func(ctx context.Context) error { return errors.New("boom") },
	}, testLogger(&out))

	waitFor(t, "error report", func() bool {
		for _, rec := range logRecords(t, &out) {
			if rec["msg"] == "channel failed" && rec["channel"] == "testchan" &&
				strings.Contains(rec["error"].(string), "boom") {
				return true
			}
		}
		return false
	})
	foundStart := false
	for _, rec := range logRecords(t, &out) {
		if rec["msg"] == "channel starting" && rec["channel"] == "testchan" {
			foundStart = true
		}
	}
	if !foundStart {
		t.Errorf("expected channel starting record, got %q", out.String())
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
	}, testLogger(&out))
	cancel()
	// Give the goroutine a chance to (incorrectly) report.
	time.Sleep(100 * time.Millisecond)
	records := logRecords(t, &out)
	if len(records) != 1 || records[0]["msg"] != "channel starting" {
		t.Errorf("expected only the startup record, got %q", out.String())
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
	}, testLogger(&out))
	cancel()
	close(release)
	time.Sleep(100 * time.Millisecond)
	for _, rec := range logRecords(t, &out) {
		if rec["msg"] == "channel failed" {
			t.Errorf("error after cancellation must not be reported, got %q", out.String())
		}
	}
}

func TestStartChannelsWithNothingConfigured(t *testing.T) {
	var out lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps := channelDeps{logger: testLogger(&out), noScheduler: true}
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
	if _, err := newTelegramChannel(config.Config{}, nil, testLogger(&out)); err == nil {
		t.Fatal("expected error for empty token")
	}
}
