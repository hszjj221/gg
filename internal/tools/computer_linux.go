//go:build linux

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
	hostname, _ := os.Hostname()

	var b strings.Builder
	fmt.Fprintf(&b, "OS: Linux\n")
	fmt.Fprintf(&b, "Arch: %s\n", runtime.GOARCH)
	if hostname != "" {
		fmt.Fprintf(&b, "Hostname: %s\n", hostname)
	}
	fmt.Fprintf(&b, "CPU cores: %d\n", runtime.NumCPU())
	if meminfo, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(meminfo), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
						fmt.Fprintf(&b, "Memory: %.1f GB\n", float64(kb)/1024/1024)
					}
				}
				break
			}
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
	_, err := runComputerCommand(ctx, "xdg-open", target)
	return err
}

func sendNotification(ctx context.Context, title, body string) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return fmt.Errorf("notify-send not found; install libnotify to use system notifications")
	}
	_, err := runComputerCommand(ctx, "notify-send", title, body)
	return err
}

// clipboardHelper picks the first available clipboard utility. X11 tools
// (xclip, xsel) come first, then the Wayland pair (wl-copy/wl-paste).
func clipboardHelper() (read, write []string, err error) {
	if _, lookErr := exec.LookPath("xclip"); lookErr == nil {
		return []string{"xclip", "-selection", "clipboard", "-o"},
			[]string{"xclip", "-selection", "clipboard", "-i"}, nil
	}
	if _, lookErr := exec.LookPath("xsel"); lookErr == nil {
		return []string{"xsel", "--clipboard", "--output"},
			[]string{"xsel", "--clipboard", "--input"}, nil
	}
	if _, rErr := exec.LookPath("wl-paste"); rErr == nil {
		if _, wErr := exec.LookPath("wl-copy"); wErr == nil {
			return []string{"wl-paste"}, []string{"wl-copy"}, nil
		}
	}
	return nil, nil, fmt.Errorf("no clipboard utility found; install xclip, xsel, or wl-clipboard")
}

func readClipboard(ctx context.Context) (string, error) {
	read, _, err := clipboardHelper()
	if err != nil {
		return "", err
	}
	return runComputerCommandOutput(ctx, read[0], read[1:]...)
}

func writeClipboard(ctx context.Context, text string) error {
	_, write, err := clipboardHelper()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, write[0], write[1:]...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", write[0], err)
	}
	return nil
}
