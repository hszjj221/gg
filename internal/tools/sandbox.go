package tools

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
)

// errSandboxScript is returned when a command is not the shell -c form
// newShellCommand builds. The caller runs the command without a sandbox.
var errSandboxScript = errors.New("shell command is not sandboxable")

// sandboxUnavailableNote is appended to bash output when --sandbox was
// requested but the OS helper cannot be used. The command still runs.
func sandboxUnavailableNote(detail string) string {
	detail = strings.ReplaceAll(detail, "\n", " ")
	if len(detail) > 180 {
		detail = detail[:180]
	}
	return "sandbox unavailable (" + detail + "); command ran without a sandbox"
}

var sandboxProbe struct {
	once sync.Once
	ok   bool
	note string
}

// sandboxReady reports whether this process can wrap commands. The probe
// runs once: a missing helper or a helper that cannot start (user
// namespaces disabled, seatbelt denied) degrades for the rest of the process.
func sandboxReady() (bool, string) {
	sandboxProbe.once.Do(func() {
		sandboxProbe.ok, sandboxProbe.note = probeOSSandbox()
	})
	return sandboxProbe.ok, sandboxProbe.note
}

// maybeSandbox returns cmd unchanged, plus a note, when the sandbox cannot
// be applied. When it can, the returned command runs cmd's shell script
// inside the OS sandbox.
func maybeSandbox(ctx context.Context, cmd *exec.Cmd, cwd string) (*exec.Cmd, string) {
	ok, note := sandboxReady()
	if !ok {
		return cmd, note
	}
	wrapped, err := wrapOSSandbox(ctx, cmd, cwd)
	if err != nil {
		return cmd, sandboxUnavailableNote(err.Error())
	}
	return wrapped, ""
}

func appendSandboxNote(text, note string) string {
	if note == "" {
		return text
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + note
}

func shellScriptOf(cmd *exec.Cmd) (shell, script string, ok bool) {
	if cmd == nil || len(cmd.Args) < 3 {
		return "", "", false
	}
	flag := cmd.Args[1]
	if flag != "-lc" && flag != "-c" {
		return "", "", false
	}
	return cmd.Args[0], cmd.Args[len(cmd.Args)-1], true
}
