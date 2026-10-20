//go:build unix

package session

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestTakeOverRefusesLeaseRenewedUnderArbitration(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "s.jsonl.lock")
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())+":fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := takeOverStaleLock(lockPath)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("takeover of a live lease = %v, want ErrLocked", err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil || !strings.Contains(string(data), ":fresh") {
		t.Fatalf("fresh lease was moved or removed: %q, %v", data, err)
	}
}

func TestTakeOverSerializedByArbitrationFile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "s.jsonl.lock")
	if err := os.WriteFile(lockPath, []byte("999999:dead"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	takeoverPath := lockPath + ".takeover"
	if err := os.WriteFile(takeoverPath, []byte(strconv.Itoa(os.Getpid())+":holder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := takeOverStaleLock(lockPath); err == nil {
		t.Fatal("takeover proceeded while arbitration was held")
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatal("stale lease was moved without holding arbitration")
	}
}

func TestTryAcquireWriterLockReturnsErrLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	release, err := tryAcquireWriterLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tryAcquireWriterLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire = %v, want ErrLocked", err)
	}
	release()
	if release2, err := tryAcquireWriterLock(path); err != nil {
		t.Fatal(err)
	} else {
		release2()
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
