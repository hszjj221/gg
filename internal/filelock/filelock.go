// Package filelock serializes read-modify-write cycles on a store directory
// across processes (the CLI and the daemon) with an exclusive flock on a
// lock file. gg officially supports Linux and macOS; other platforms get an
// explicit error instead of silent corruption.
package filelock

import (
	"fmt"
	"os"
)

// Lock takes an exclusive lock on path (created when missing) and returns
// an unlock function. The caller must call unlock exactly once.
func Lock(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire file lock: %w", err)
	}
	return func() {
		_ = unlockFile(f)
		f.Close()
	}, nil
}
