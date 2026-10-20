//go:build !unix

package daemon

import (
	"errors"
	"os"
)

var errPIDLockUnsupported = errors.New("pidfile locking is unsupported")

func lockPIDFile(*os.File) error    { return errPIDLockUnsupported }
func unlockPIDFile(*os.File) error  { return nil }
func pidFileLockHeld(*os.File) bool { return false }
