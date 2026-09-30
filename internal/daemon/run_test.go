package daemon

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testStdinLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
