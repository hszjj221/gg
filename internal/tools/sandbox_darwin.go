//go:build darwin

package tools

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

func probeOSSandbox() (bool, string) {
	path, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return false, sandboxUnavailableNote("sandbox-exec not found")
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false, sandboxUnavailableNote("working directory: " + err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, darwinSandboxArgs(cwd, shell, "true")...)
	if err := cmd.Run(); err != nil {
		return false, sandboxUnavailableNote("sandbox-exec: " + err.Error())
	}
	return true, ""
}

func wrapOSSandbox(ctx context.Context, cmd *exec.Cmd, cwd string) (*exec.Cmd, error) {
	path, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return nil, err
	}
	shell, script, ok := shellScriptOf(cmd)
	if !ok {
		return nil, errSandboxScript
	}
	wrapped := exec.CommandContext(ctx, path, darwinSandboxArgs(cwd, shell, script)...)
	wrapped.Dir = cwd
	wrapped.Env = cmd.Env
	return wrapped, nil
}

// darwinSandboxArgs allows reads everywhere and writes only under cwd, /tmp,
// and the per-user temporary directory macOS uses. Network stays open.
func darwinSandboxArgs(cwd, shell, script string) []string {
	return []string{"-p", darwinSandboxProfile(cwd), shell, "-c", script}
}

func darwinSandboxProfile(cwd string) string {
	cwd = schemeQuote(cwd)
	return `(version 1)
(allow default)
(deny file-write*)
(allow file-write* (subpath "` + cwd + `"))
(allow file-write* (subpath "/dev"))
(allow file-write* (subpath "/tmp"))
(allow file-write* (subpath "/private/tmp"))
(allow file-write* (subpath "/private/var/folders"))
`
}

func schemeQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
