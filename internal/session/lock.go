package session

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	lockWaitTimeout = 5 * time.Second
	staleLockAge    = 30 * time.Second
	lockPollDelay   = 10 * time.Millisecond
)

// acquireWriterLock uses an O_EXCL sidecar lease so it works on every platform
// supported by Go without platform-specific flock implementations. Session
// writes are short; a stale lease is recoverable after staleLockAge.
func acquireWriterLock(sessionPath string) (func(), error) {
	lockPath := sessionPath + ".lock"
	token := fmt.Sprintf("%d:%s", os.Getpid(), newID())
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, err = file.WriteString(token); err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				_ = os.Remove(lockPath)
				if err != nil {
					return nil, err
				}
				return nil, closeErr
			}
			return func() {
				data, readErr := os.ReadFile(lockPath)
				if readErr == nil && strings.TrimSpace(string(data)) == token {
					_ = os.Remove(lockPath)
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		info, statErr := os.Stat(lockPath)
		if statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
		}
		time.Sleep(lockPollDelay)
	}
}
