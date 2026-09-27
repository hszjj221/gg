package cliapp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/browser"
)

const browserUsage = `usage: gg browser <shot|read> <url>

  shot <url>   load the page in headless Chromium, save a PNG screenshot
  read <url>   load the page in headless Chromium, print its rendered text

Needs a Chromium binary (chromium / google-chrome, or GG_CHROMIUM).
Screenshots go to ~/.gg/media/screenshots/.`

func runBrowserCommand(ctx context.Context, browserArgs []string, stdout, stderr io.Writer) int {
	if len(browserArgs) < 2 {
		fmt.Fprintln(stdout, browserUsage)
		return 2
	}
	url := browserArgs[1]
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		fmt.Fprintln(stderr, "gg browser: only http(s) URLs are allowed")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	s, err := browser.Start(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer s.Close()
	if err := s.Navigate(ctx, url); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch browserArgs[0] {
	case "shot":
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".gg", "media", "screenshots")
		path, err := s.ScreenshotToFile(ctx, dir)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, path)
		return 0
	case "read":
		text, err := s.Text(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, text)
		return 0
	default:
		fmt.Fprintln(stderr, browserUsage)
		return 2
	}
}
