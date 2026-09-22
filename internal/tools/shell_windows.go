//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	if shell := os.Getenv("SHELL"); shell != "" {
		return exec.CommandContext(ctx, shell, "-lc", command)
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, shell)
	// cmd.exe does not use CommandLineToArgvW-compatible quoting. Supplying its
	// raw command line prevents Go from backslash-escaping quotes in commands.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `/d /s /c "` + command + `"`}
	return cmd
}
