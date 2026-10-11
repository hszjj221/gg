//go:build !linux && !darwin

package tools

import (
	"context"
	"os/exec"
)

func probeOSSandbox() (bool, string) {
	return false, sandboxUnavailableNote("no sandbox helper on this platform")
}

func wrapOSSandbox(context.Context, *exec.Cmd, string) (*exec.Cmd, error) {
	return nil, errSandboxScript
}
