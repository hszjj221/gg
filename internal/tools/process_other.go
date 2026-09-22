//go:build !unix && !windows

package tools

import (
	"os/exec"
	"time"
)

func configureProcess(cmd *exec.Cmd) { cmd.WaitDelay = 200 * time.Millisecond }
