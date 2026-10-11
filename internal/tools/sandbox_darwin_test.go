//go:build darwin

package tools

import (
	"strings"
	"testing"
)

func TestDarwinSandboxProfileAllowsWorkdirWrites(t *testing.T) {
	profile := darwinSandboxProfile("/work/proj")
	if !strings.Contains(profile, `(deny file-write*)`) || !strings.Contains(profile, `/work/proj`) {
		t.Fatalf("profile = %s", profile)
	}
	quoted := darwinSandboxProfile(`/work/"quoted"`)
	if !strings.Contains(quoted, `\"quoted\"`) {
		t.Fatalf("profile did not escape quotes: %s", quoted)
	}
}
