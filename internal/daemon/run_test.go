package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

func testStdinLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type stubProvider struct{}

func (stubProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	return agent.AssistantMessage{}, nil
}

func testRunOptions(home string, stderr *bytes.Buffer) Options {
	return Options{
		Stdin:           strings.NewReader(""),
		Stderr:          stderr,
		Stdout:          io.Discard,
		HomeDir:         home,
		ProviderFactory: func(config.Config) agent.Provider { return stubProvider{} },
	}
}

// A pidfile failure that is not contention must fail closed: starting
// without the single-instance guard would risk duplicate channels.
func TestRunRefusesStartWhenInstanceLockUnavailable(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gg")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory where the pid file should be makes the lock
	// un-acquirable for a non-contention reason.
	if err := os.MkdirAll(filepath.Join(dir, "ggd.pid"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := Run(context.Background(),
		[]string{"--http", "127.0.0.1:0", "--token", "test-token", "--no-scheduler"},
		testRunOptions(home, &stderr))
	if code != 1 {
		t.Fatalf("Run exit code = %d, want 1 (fail closed)", code)
	}
	if !strings.Contains(stderr.String(), "cannot acquire instance lock") {
		t.Errorf("stderr = %q, want the lock failure explained", stderr.String())
	}
}

// A parent-supervised sidecar (--exit-on-stdin-eof, e.g. the Electron app)
// must stay usable when a service daemon already owns the channels: it
// serves its API without starting scheduler/messaging channels instead of
// refusing to start or doubling every channel.
func TestRunSidecarDegradesWhenInstanceLockHeld(t *testing.T) {
	home := t.TempDir()
	holder, err := WritePidFile(home)
	if err != nil {
		t.Fatal(err)
	}
	defer holder()
	var stderr bytes.Buffer
	code := Run(context.Background(),
		[]string{"--http", "127.0.0.1:0", "--token", "test-token", "--no-scheduler", "--exit-on-stdin-eof"},
		testRunOptions(home, &stderr))
	if code != 0 {
		t.Fatalf("sidecar Run exit code = %d, want 0 (degraded, not refused)", code)
	}
	if !strings.Contains(stderr.String(), "without channels") {
		t.Errorf("stderr = %q, want the channel-less degradation logged", stderr.String())
	}
}

// Without the sidecar flag, a second full daemon is still refused outright.
func TestRunRefusesSecondFullDaemon(t *testing.T) {
	home := t.TempDir()
	holder, err := WritePidFile(home)
	if err != nil {
		t.Fatal(err)
	}
	defer holder()
	var stderr bytes.Buffer
	code := Run(context.Background(),
		[]string{"--http", "127.0.0.1:0", "--token", "test-token", "--no-scheduler"},
		testRunOptions(home, &stderr))
	if code != 1 {
		t.Fatalf("second daemon Run exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "already running") {
		t.Errorf("stderr = %q, want the already-running refusal", stderr.String())
	}
}

func TestWatchStdinEOFCancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// strings.Reader hits EOF immediately: simulates the parent dying and
	// the control pipe closing.
	go watchStdinEOF(strings.NewReader(""), testStdinLogger(), cancel)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context not cancelled after stdin EOF")
	}
}

func TestWatchStdinEOFWaitsForEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	go watchStdinEOF(reader, testStdinLogger(), cancel)
	// Data on the control pipe must not shut the daemon down; only EOF does.
	if _, err := writer.Write([]byte("noise")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("context cancelled while stdin still open")
	case <-time.After(200 * time.Millisecond):
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context not cancelled after stdin EOF")
	}
}
