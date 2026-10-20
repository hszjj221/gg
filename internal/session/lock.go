package session

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	lockWaitTimeout  = 5 * time.Second
	staleLockAge     = 30 * time.Second
	takeoverStaleAge = 10 * time.Second
	lockPollDelay    = 10 * time.Millisecond
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
			if err := takeOverStaleLock(lockPath); err != nil {
				if errors.Is(err, ErrLocked) || os.IsExist(err) {
					if time.Now().After(deadline) {
						return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
					}
					time.Sleep(lockPollDelay)
					continue
				}
				return nil, err
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
	if err := takeOverStaleLock(lockPath); err != nil {
		if errors.Is(err, ErrLocked) || os.IsExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
		}
		return nil, err
	}
	release, err = createLockFile(lockPath, token)
	if err != nil && os.IsExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrLocked, sessionPath)
	}
	return release, err
}

// takeOverStaleLock serializes replacement of a dead lease. Callers hold no
// lease and retry createLockFile after a nil return. The arbitration file
// makes check-then-rename atomic between contenders: only the holder may
// move the lease, and it re-checks staleness after acquiring arbitration.
func takeOverStaleLock(lockPath string) error {
	takeoverPath := lockPath + ".takeover"
	token := fmt.Sprintf("%d:%s", os.Getpid(), newID())
	release, err := createLockFile(takeoverPath, token)
	if err != nil {
		if os.IsExist(err) {
			clearAbandonedTakeover(takeoverPath)
		}
		return err
	}
	defer release()
	if !lockIsStale(lockPath, time.Now()) {
		// The lease was renewed or removed while we waited for arbitration.
		return fmt.Errorf("%w: %s", ErrLocked, lockPath)
	}
	stalePath := lockPath + ".stale." + newID()
	if err := os.Rename(lockPath, stalePath); err != nil {
		return err
	}
	_ = os.Remove(stalePath)
	return nil
}

// clearAbandonedTakeover removes only an aged arbitration file whose owner
// is gone. The critical section is microseconds; a live holder is never
// displaced. PID reuse is the residual risk, same as the lease itself.
func clearAbandonedTakeover(takeoverPath string) {
	info, err := os.Stat(takeoverPath)
	if err != nil || time.Since(info.ModTime()) <= takeoverStaleAge {
		return
	}
	if pid, ok := lockOwnerPID(takeoverPath); ok && processAlive(pid) {
		return
	}
	_ = os.Remove(takeoverPath)
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
