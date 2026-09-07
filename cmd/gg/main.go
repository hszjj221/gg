package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/hszjj221/gg/internal/app"
)

const version = "0.1.0"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	code := app.Run(ctx, os.Args[1:], app.Options{Version: version})
	cancel()
	os.Exit(code)
}
