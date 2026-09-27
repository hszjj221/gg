package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeJobName(t *testing.T) {
	cases := map[string]string{
		"morning-brief":   "morning-brief",
		"早间简报":            "早间简报",
		"weird / name: 1": "weird-name-1",
		"":                "job",
		"a":               "a",
	}
	for in, want := range cases {
		if got := sanitizeJobName(in); got != want {
			t.Errorf("sanitizeJobName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeJobName("x"); len(got) > 32 {
		t.Errorf("name too long: %q", got)
	}
	long := "abcdefghijklmnopqrstuvwxyz-0123456789-extra"
	if got := sanitizeJobName(long); len(got) != 32 {
		t.Errorf("long name not truncated to 32: %q", got)
	}
}

func TestPidFileLifecycle(t *testing.T) {
	home := t.TempDir()
	if DaemonAlive(home) {
		t.Fatal("daemon should not be alive before pid file exists")
	}
	cleanup, err := WritePidFile(home)
	if err != nil {
		t.Fatal(err)
	}
	if !DaemonAlive(home) {
		t.Fatal("daemon should be alive after writing pid file")
	}
	cleanup()
	if DaemonAlive(home) {
		t.Fatal("daemon should not be alive after cleanup")
	}
	// A stale pid file for a dead process must not read as alive.
	if err := writePidFileForTest(t, home); err != nil {
		t.Fatal(err)
	}
	if DaemonAlive(home) {
		t.Fatal("stale pid file must not read as alive")
	}
}

func TestWritePidFileEmptyHome(t *testing.T) {
	if _, err := WritePidFile(""); err == nil {
		t.Fatal("expected error for empty home")
	}
}

func writePidFileForTest(t *testing.T, home string) error {
	t.Helper()
	// 2^31-1 is not a live pid on any real system.
	return os.WriteFile(filepath.Join(home, "ggd.pid"), []byte("2147483647\n"), 0o644)
}
