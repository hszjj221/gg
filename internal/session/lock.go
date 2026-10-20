package session

import (
	"fmt"
	"os"
	"strconv"
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
// writes are short; a stale lease is recoverable after staleLockAge once its
// owner is confirmed dead.
func acquireWriterLock(sessionPath string) (func(), error) {
	lockPath := sessionPath + ".lock"
	token := fmt.Sprintf("%d:%s", os.Getpid(), newID())
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		release, err := createLockFile(lockPath, token)
		if err == nil {
			return release, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if lockIsStale(lockPath, time.Now()) {
			// Move the old lease out of the way atomically. Only one
			// contender can rename it; a successor's fresh lock at the
			// original path is never removed by this code.
			stalePath := lockPath + ".stale." + newID()
			if renameErr := os.Rename(lockPath, stalePath); renameErr == nil {
				_ = os.Remove(stalePath)
			}
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
		}
		time.Sleep(lockPollDelay)
	}
}

// tryAcquireWriterLock is acquireWriterLock without the retry loop. A live
// owner yields ErrLocked immediately; an aged lease whose owner is gone is
// taken over once.
func tryAcquireWriterLock(sessionPath string) (func(), error) {
	lockPath := sessionPath + ".lock"
	token := fmt.Sprintf("%d:%s", os.Getpid(), newID())
	release, err := createLockFile(lockPath, token)
	if err == nil {
		return release, nil
	}
	if !os.IsExist(err) {
		return nil, err
	}
	if !lockIsStale(lockPath, time.Now()) {
		return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
	}
	stalePath := lockPath + ".stale." + newID()
	if err := os.Rename(lockPath, stalePath); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
	}
	_ = os.Remove(stalePath)
	return createLockFile(lockPath, token)
}

func createLockFile(lockPath, token string) (func(), error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
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

func lockIsStale(path string, now time.Time) bool {
	info, err := os.Stat(path)
	if err != nil || now.Sub(info.ModTime()) <= staleLockAge {
		return false
	}
	pid, ok := lockOwnerPID(path)
	if !ok {
		// Unreadable/corrupt lease: it cannot name a live owner, so it is
		// recoverable exactly like an aged lock.
		return true
	}
	return !processAlive(pid)
}

func lockOwnerPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	head, _, ok := strings.Cut(strings.TrimSpace(string(data)), ":")
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(head)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}
