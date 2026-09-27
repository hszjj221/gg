package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// pidFileName is written under ~/.gg while ggd is alive so the CLI can tell
// whether scheduled jobs have a running daemon to fire them.
const pidFileName = "ggd.pid"

// WritePidFile records the current process id under ~/.gg/ggd.pid and returns
// a cleanup function that removes it. A stale file from a crashed daemon is
// overwritten.
func WritePidFile(home string) (func(), error) {
	if home == "" {
		return nil, fmt.Errorf("home dir is required")
	}
	path := filepath.Join(home, ".gg", pidFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, err
	}
	return func() { os.Remove(path) }, nil
}

// DaemonAlive reports whether a ggd process appears to be running, based on
// the pid file and a kill(0) probe. It is advisory: false negatives are
// possible if the pid file was removed out of band.
func DaemonAlive(home string) bool {
	if home == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(home, ".gg", pidFileName))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
