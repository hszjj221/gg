//go:build darwin

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// runComputerCommand runs a helper subprocess with a bounded context and
// returns its trimmed stdout.
func runComputerCommand(ctx context.Context, name string, args ...string) (string, error) {
	out, err := runComputerCommandOutput(ctx, name, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// runComputerCommandOutput is runComputerCommand without trimming: clipboard
// reads must preserve the exact content, including boundary whitespace.
func runComputerCommandOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}

func computerInfoText(ctx context.Context) (string, error) {
	hostname, _ := runComputerCommand(ctx, "hostname")
	cpuCount := runtime.NumCPU()
	memBytes, memErr := runComputerCommand(ctx, "sysctl", "-n", "hw.memsize")
	cpuBrand, _ := runComputerCommand(ctx, "sysctl", "-n", "machdep.cpu.brand_string")
	osVersion, _ := runComputerCommand(ctx, "sw_vers", "-productVersion")

	var b strings.Builder
	fmt.Fprintf(&b, "OS: macOS %s\n", osVersion)
	fmt.Fprintf(&b, "Arch: %s\n", runtime.GOARCH)
	if hostname != "" {
		fmt.Fprintf(&b, "Hostname: %s\n", hostname)
	}
	if cpuBrand != "" {
		fmt.Fprintf(&b, "CPU: %s (%d cores)\n", cpuBrand, cpuCount)
	} else {
		fmt.Fprintf(&b, "CPU cores: %d\n", cpuCount)
	}
	if memErr == nil {
		if n, err := strconv.ParseUint(strings.TrimSpace(memBytes), 10, 64); err == nil {
			fmt.Fprintf(&b, "Memory: %.1f GB\n", float64(n)/1024/1024/1024)
		}
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs("/", &fs); err == nil {
		total := uint64(fs.Bsize) * fs.Blocks
		free := uint64(fs.Bsize) * fs.Bavail
		fmt.Fprintf(&b, "Disk (/): %.1f GB total, %.1f GB available\n",
			float64(total)/1024/1024/1024, float64(free)/1024/1024/1024)
	}
	return b.String(), nil
}

func listProcesses(ctx context.Context) ([]processInfo, error) {
	out, err := runComputerCommand(ctx, "ps", "-eo", "pid,comm,pcpu,pmem")
	if err != nil {
		return nil, err
	}
	return parsePS(out), nil
}

func killProcess(pid int, force bool) error {
	// os.FindProcess on Unix always succeeds; the signal itself fails
	// with ESRCH when the pid does not exist.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if force {
		return proc.Kill()
	}
	return proc.Signal(syscall.SIGTERM)
}

func openTarget(ctx context.Context, target string) error {
	_, err := runComputerCommand(ctx, "open", target)
	return err
}

// appleScriptString escapes a Go string for embedding in an AppleScript
// double-quoted string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func sendNotification(ctx context.Context, title, body string) error {
	// Single escaping: appleScriptString already produces an AppleScript
	// string literal body, so %q (which would escape the backslashes again)
	// must not be applied on top of it.
	script := fmt.Sprintf("display notification \"%s\" with title \"%s\"",
		appleScriptString(body), appleScriptString(title))
	_, err := runComputerCommand(ctx, "osascript", "-e", script)
	return err
}

func readClipboard(ctx context.Context) (string, error) {
	return runComputerCommandOutput(ctx, "pbpaste")
}

func writeClipboard(ctx context.Context, text string) error {
	cmd := exec.CommandContext(ctx, "pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}
	return nil
}
