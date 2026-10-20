package session

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLockIsStaleRequiresDeadOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl.lock")
	if err := os.WriteFile(path, []byte("999999:token"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if !lockIsStale(path, time.Now()) {
		t.Fatal("old lock held by a dead pid should be stale")
	}
}

func TestLockIsNotStaleWhileOwnerIsAlive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl.lock")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+":token"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if lockIsStale(path, time.Now()) {
		t.Fatal("a live process's lock must not be stolen")
	}
}

func TestAcquireWriterLockTakesOverDeadLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, []byte("999999:token"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := acquireWriterLock(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
