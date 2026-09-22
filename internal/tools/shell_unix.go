//go:build !windows

package tools

import (
	"context"
	"os"
	"os/exec"
)

func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return exec.CommandContext(ctx, shell, "-lc", command)
}
