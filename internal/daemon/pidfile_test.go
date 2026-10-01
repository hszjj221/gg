package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A second daemon in the same process must be refused while the first holds
// the pidfile lock; after cleanup the lock is free again.
func TestWritePidFileSingleInstance(t *testing.T) {
	home := t.TempDir()
	cleanup, err := WritePidFile(home)
	if err != nil {
		t.Fatal(err)
	}
	if !DaemonAlive(home) {
		t.Fatal("daemon should be alive while holding the pidfile lock")
	}
	if _, err := WritePidFile(home); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second instance must get ErrAlreadyRunning, got %v", err)
	}
	// The failed second attempt must not disturb the first holder.
	if !DaemonAlive(home) {
		t.Fatal("first holder must still be alive after refused second attempt")
	}
	cleanup()
	if DaemonAlive(home) {
		t.Fatal("daemon should not be alive after cleanup releases the lock")
	}
	// The lock is free again: a successor can start.
	cleanup2, err := WritePidFile(home)
	if err != nil {
		t.Fatalf("successor must acquire the lock after cleanup: %v", err)
	}
	cleanup2()
}

// A stale pid file from a crashed daemon (no lock held) must not read as
// alive and must not block a fresh start.
func TestWritePidFileStaleFile(t *testing.T) {
	home := t.TempDir()
	if err := writePidFileForTest(t, home); err != nil {
		t.Fatal(err)
	}
	if DaemonAlive(home) {
		t.Fatal("stale pid file without a lock holder must not read as alive")
	}
	cleanup, err := WritePidFile(home)
	if err != nil {
		t.Fatalf("fresh start must succeed over a stale pid file: %v", err)
	}
	cleanup()
}

// State left behind by older versions (0755 directory, 0644 pid file) must
// be tightened to 0700/0600 on startup: the create modes alone only apply
// to new paths.
func TestWritePidFileTightensExistingPermissions(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(dir, "ggd.pid")
	if err := os.WriteFile(pidPath, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanup, err := WritePidFile(home)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if fi, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o700 {
		t.Errorf("existing .gg dir mode = %o, want 700", fi.Mode().Perm())
	}
	if fi, err := os.Stat(pidPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("existing pid file mode = %o, want 600", fi.Mode().Perm())
	}
}
