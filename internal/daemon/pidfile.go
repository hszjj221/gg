package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// pidFileName is written under ~/.gg while ggd is alive so the CLI can tell
// whether scheduled jobs have a running daemon to fire them.
const pidFileName = "ggd.pid"

// ErrAlreadyRunning is returned by WritePidFile when another ggd process
// holds the pidfile lock. The caller must refuse to start: two daemons
// would double-fire scheduled jobs and double-reply Telegram messages.
var ErrAlreadyRunning = errors.New("another ggd is already running")

// WritePidFile takes an exclusive, non-blocking flock on ~/.gg/ggd.pid and
// holds it until the returned cleanup runs. The lock — not the pid inside —
// is the single-instance guard: it is released by the kernel even if the
// daemon crashes, so a stale file can never block a fresh start. A second
// daemon racing here gets ErrAlreadyRunning.
//
// The ~/.gg directory and the pid file are owner-only (0700/0600): the pid
// file lives next to credentials and session data.
func WritePidFile(home string) (func(), error) {
	if home == "" {
		return nil, fmt.Errorf("home dir is required")
	}
	dir := filepath.Join(home, ".gg")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll leaves an existing directory's mode alone, so enforce
	// owner-only explicitly: upgrades from older versions (0755) get
	// tightened instead of staying world-readable.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, pidFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	// Same for a pid file left behind by an older version (0644): the
	// create mode only applies to new files.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock pid file: %w", err)
	}
	// Truncate and rewrite under the lock so a concurrent DaemonAlive probe
	// never reads a half-written pid.
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		_ = f.Close()
		return nil, err
	}
	// Closing the fd releases the lock. The file itself is intentionally
	// left behind: removing it would race with a successor that already
	// locked the (then unlinked) inode, breaking the single-instance
	// guarantee. DaemonAlive probes the lock, not the file's presence.
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// DaemonAlive reports whether a ggd process appears to be running. It probes
// the pidfile lock: if the lock is held, a daemon is alive; if it can be
// acquired, no daemon holds it (a stale file from a crashed daemon reads as
// not alive). The pid inside is advisory only.
func DaemonAlive(home string) bool {
	if home == "" {
		return false
	}
	f, err := os.OpenFile(filepath.Join(home, ".gg", pidFileName), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
}
