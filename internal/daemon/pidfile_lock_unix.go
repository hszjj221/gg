//go:build unix

package daemon

import (
	"errors"
	"os"
	"syscall"
)

var errPIDLockUnsupported = errors.New("pidfile locking is unsupported")

func lockPIDFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrAlreadyRunning
	}
	return err
}

func unlockPIDFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func pidFileLockHeld(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
}
