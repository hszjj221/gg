package main

import (
	"context"
	"os"

	"gg/internal/app"
)

const version = "0.1.0"

func main() {
	os.Exit(app.Run(context.Background(), os.Args[1:], app.Options{Version: version}))
}
