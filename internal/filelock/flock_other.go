//go:build !unix

package filelock

import (
	"errors"
	"os"
)

func lockFile(f *os.File) error {
	return errors.New("file locking is not supported on this platform")
}

func unlockFile(f *os.File) error {
	return errors.New("file locking is not supported on this platform")
}
