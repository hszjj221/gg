//go:build linux

package tools

import (
	"context"
	"os"
	"os/exec"
	"time"
)

func probeOSSandbox() (bool, string) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return false, sandboxUnavailableNote("bubblewrap not found")
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, linuxBwrapArgs(cwd, shell, "true")...)
	if err := cmd.Run(); err != nil {
		return false, sandboxUnavailableNote("bubblewrap: " + err.Error())
	}
	return true, ""
}

func wrapOSSandbox(ctx context.Context, cmd *exec.Cmd, cwd string) (*exec.Cmd, error) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, err
	}
	shell, script, ok := shellScriptOf(cmd)
	if !ok {
		return nil, errSandboxScript
	}
	wrapped := exec.CommandContext(ctx, path, linuxBwrapArgs(cwd, shell, script)...)
	wrapped.Dir = cwd
	wrapped.Env = cmd.Env
	return wrapped, nil
}

// linuxBwrapArgs builds a bubblewrap invocation that can read the system
// and write only inside cwd. /tmp is a fresh tmpfs. The tmpfs is mounted
// before cwd is bound: bubblewrap applies mounts in argument order, and a
// later --tmpfs /tmp would hide a workspace that lives under /tmp.
// Network is left shared: this is a filesystem-write sandbox, not a net jail.
func linuxBwrapArgs(cwd, shell, script string) []string {
	args := []string{
		"--die-with-parent",
		"--ro-bind", "/", "/",
		"--tmpfs", "/tmp",
		"--bind", cwd, cwd,
		"--dev", "/dev",
		"--proc", "/proc",
		"--chdir", cwd,
		"--",
		shell, "-c", script,
	}
	return args
}
