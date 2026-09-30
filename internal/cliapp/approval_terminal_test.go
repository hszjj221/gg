package cliapp

import (
	"io"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/cli"
)

func approvalTestStreams(t *testing.T, stdinTerm, stdoutTerm, stderrTerm bool) (io.Reader, io.Writer, io.Writer, func(any) bool) {
	t.Helper()
	stdin := strings.NewReader("")
	var stdout, stderr strings.Builder
	terminals := map[any]bool{stdin: stdinTerm, &stdout: stdoutTerm, &stderr: stderrTerm}
	return stdin, &stdout, &stderr, func(v any) bool { return terminals[v] }
}

func TestApprovalTerminalAvailableInteractiveRequiresStderr(t *testing.T) {
	interactive := cli.Args{} // Prompt == "" && !Print

	// The reported bug: stdin+stdout are terminals but stderr is redirected.
	// The prompt is written to stderr, so the user would never see it while
	// the CLI blocks on stdin.
	stdin, stdout, stderr, isTerm := approvalTestStreams(t, true, true, false)
	if approvalTerminalAvailable(interactive, stdin, stdout, stderr, isTerm) {
		t.Fatal("interactive approval must be unavailable when stderr is redirected")
	}

	stdin, stdout, stderr, isTerm = approvalTestStreams(t, true, true, true)
	if !approvalTerminalAvailable(interactive, stdin, stdout, stderr, isTerm) {
		t.Fatal("interactive approval must be available when stdin/stdout/stderr are terminals")
	}
}

func TestLineApprovalTerminals(t *testing.T) {
	stdin, _, stderr, isTerm := approvalTestStreams(t, true, false, true)
	if !lineApprovalTerminals(stdin, stderr, isTerm) {
		t.Fatal("line approval needs only stdin+stderr, not stdout")
	}

	stdin, _, stderr, isTerm = approvalTestStreams(t, true, false, false)
	if lineApprovalTerminals(stdin, stderr, isTerm) {
		t.Fatal("line approval must be unavailable when stderr is redirected")
	}
}
