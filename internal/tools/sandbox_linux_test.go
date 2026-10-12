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
	if !tmpfsBeforeBind(args, "/work") {
		t.Fatalf("tmpfs /tmp must be mounted before binding cwd:\n%s", joined)
	}
}

func TestLinuxBwrapArgsTmpfsBeforeTmpWorkdir(t *testing.T) {
	cwd := "/tmp/work"
	args := linuxBwrapArgs(cwd, "/bin/sh", "true")
	if !tmpfsBeforeBind(args, cwd) {
		t.Fatalf("workspace under /tmp is hidden when --tmpfs follows --bind: %v", args)
	}
}

func tmpfsBeforeBind(args []string, cwd string) bool {
	tmpfsAt := -1
	bindAt := -1
	for i := 0; i < len(args); i++ {
		if args[i] == "--tmpfs" && i+1 < len(args) && args[i+1] == "/tmp" && tmpfsAt < 0 {
			tmpfsAt = i
		}
		if args[i] == "--bind" && i+2 < len(args) && args[i+1] == cwd && args[i+2] == cwd && bindAt < 0 {
			bindAt = i
		}
	}
	return tmpfsAt >= 0 && bindAt > tmpfsAt
}
