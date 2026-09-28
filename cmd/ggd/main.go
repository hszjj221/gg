package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hszjj221/gg/internal/daemon"
)

const version = "0.1.0"

func main() {
	// Service managers (systemd, launchd) stop with SIGTERM by default; it
	// must cancel the daemon context so HTTP shutdown, pidfile cleanup,
	// and scheduler bookkeeping run instead of dying mid-flight.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := daemon.Run(ctx, os.Args[1:], daemon.Options{Version: version})
	cancel()
	os.Exit(code)
}
