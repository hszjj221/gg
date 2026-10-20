//go:build !unix

package session

// processAlive is conservative on platforms without a cheap liveness
// probe: report true so a lock is never stolen on an assumption we cannot
// verify. gg officially supports Linux and macOS.
func processAlive(pid int) bool { return true }
