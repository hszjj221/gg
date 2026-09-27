//go:build !unix

package scheduler

import (
	"errors"
	"os"
)

func lockFile(f *os.File) error {
	return errors.New("scheduler file locking is not supported on this platform")
}

func unlockFile(f *os.File) error {
	return errors.New("scheduler file locking is not supported on this platform")
}
