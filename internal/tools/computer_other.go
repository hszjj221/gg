//go:build !linux && !darwin && !windows

package tools

import (
	"context"
	"errors"
)

// The common computer.go references these backend symbols unconditionally,
// so every other Unix target (e.g. FreeBSD) needs definitions to compile.
// The registry degrades the whole computer capability to absent off
// Linux/macOS, so these stubs are never reached at runtime.

var errComputerUnsupported = errors.New("computer tools are not supported on this platform")

func computerInfoText(ctx context.Context) (string, error) { return "", errComputerUnsupported }

func listProcesses(ctx context.Context) ([]processInfo, error) { return nil, errComputerUnsupported }

func killProcess(pid int, force bool) error { return errComputerUnsupported }

func openTarget(ctx context.Context, target string) error { return errComputerUnsupported }

func sendNotification(ctx context.Context, title, body string) error { return errComputerUnsupported }

func readClipboard(ctx context.Context) (string, error) { return "", errComputerUnsupported }

func writeClipboard(ctx context.Context, text string) error { return errComputerUnsupported }
