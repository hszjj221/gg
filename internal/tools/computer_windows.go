//go:build windows

package tools

import (
	"context"
	"errors"
)

var errComputerUnsupported = errors.New("computer tools are not supported on Windows yet")

func computerInfoText(ctx context.Context) (string, error) { return "", errComputerUnsupported }

func listProcesses(ctx context.Context) ([]processInfo, error) { return nil, errComputerUnsupported }

func killProcess(pid int, force bool) error { return errComputerUnsupported }

func openTarget(ctx context.Context, target string) error { return errComputerUnsupported }

func sendNotification(ctx context.Context, title, body string) error { return errComputerUnsupported }

func readClipboard(ctx context.Context) (string, error) { return "", errComputerUnsupported }

func writeClipboard(ctx context.Context, text string) error { return errComputerUnsupported }
