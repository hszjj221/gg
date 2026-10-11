//go:build linux

package tools

import (
	"strings"
	"testing"
)

func TestLinuxBwrapArgsBindWorkdir(t *testing.T) {
	args := linuxBwrapArgs("/work", "/bin/sh", "printf ok")
	joined := strings.Join(args, "\n")
	for _, want := range []string{"--ro-bind", "--bind", "/work", "--tmpfs", "/tmp", "--die-with-parent", "/bin/sh", "-c", "printf ok"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("bwrap args missing %q:\n%s", want, joined)
		}
	}
}
