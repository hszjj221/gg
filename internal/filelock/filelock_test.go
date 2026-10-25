//go:build unix

package filelock

import (
	"path/filepath"
	"testing"
)

func TestLockUnlock(t *testing.T) {
	unlock, err := Lock(filepath.Join(t.TempDir(), "store.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if unlock == nil {
		t.Fatal("Lock returned a nil unlock function")
	}
	unlock()
}

func TestLockSequential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.lock")
	for i := 0; i < 2; i++ {
		unlock, err := Lock(path)
		if err != nil {
			t.Fatalf("Lock #%d: %v", i+1, err)
		}
		unlock()
	}
}
