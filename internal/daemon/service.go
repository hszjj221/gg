package daemon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Service installation keeps ggd alive across crashes and reboots: without
// it, a dead daemon silently stops the scheduler, Telegram polling, and
// every other channel. install-service writes an OS-native service
// definition (systemd user unit on Linux, launchd agent on macOS) that
// restarts ggd on failure; uninstall-service removes it.
//
// Usage: ggd install-service -- --http <addr> [--token <redacted>] [daemon flags...]
// Everything after the subcommand is baked verbatim into the service's
// start command, e.g.:
//	ggd install-service -- --http 127.0.0.1:8765 --token <redacted>
//	ggd uninstall-service
//
// --http is required: a service has no terminal, so without it the daemon
// would run in stdio mode and exit immediately. The directory where
// install-service runs is baked in as --cwd so scheduled jobs bound to that
// workspace keep firing.

const (
	systemdUnitName = "ggd.service"
	launchdLabel    = "com.gg.ggd"
)

// serviceFile returns the path of the OS-native service definition for the
// given home directory, or an error on unsupported platforms. gg does not
// support Windows, so only Linux (systemd) and macOS (launchd) are covered.
func serviceFile(home string) (string, error) {
	switch runtime.GOOS {
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", systemdUnitName), nil
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
	default:
		return "", fmt.Errorf("service installation is not supported on %s (linux and darwin only)", runtime.GOOS)
	}
}

// daemonCommand resolves the current executable and appends the daemon flags
// the service should start with.
func daemonCommand(args []string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve ggd executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return append([]string{exe}, args...), nil
}

func systemdUnit(command []string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=gg daemon (personal AI agent)\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("ExecStart=")
	b.WriteString(shellQuoteCommand(command))
	b.WriteString("\nRestart=always\n")
	b.WriteString("RestartSec=5\n")
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

func launchdPlist(command []string, logPath string) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("\t<key>Label</key><string>" + launchdLabel + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range command {
		b.WriteString("\t\t<string>" + plistEscape(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key><true/>\n")
	b.WriteString("\t<key>KeepAlive</key><true/>\n")
	b.WriteString("\t<key>StandardOutPath</key><string>" + plistEscape(logPath) + "</string>\n")
	b.WriteString("\t<key>StandardErrorPath</key><string>" + plistEscape(logPath) + "</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func shellQuoteCommand(command []string) string {
	quoted := make([]string, len(command))
	for i, arg := range command {
		if strings.ContainsAny(arg, " \t\"'\\$`!") {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}

func plistEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func installService(stdout, stderr io.Writer, home string, args []string) int {
	path, err := serviceFile(home)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// A service has no terminal: without --http the daemon would enter
	// stdio mode, hit EOF on stdin, and exit immediately, turning
	// Restart=always / KeepAlive into a respawn loop. Refuse such commands.
	hasHTTP := false
	for _, a := range args {
		if a == "--http" || strings.HasPrefix(a, "--http=") {
			hasHTTP = true
			break
		}
	}
	if !hasHTTP {
		fmt.Fprintln(stderr, "install-service requires --http <addr>: without it the daemon runs in stdio mode and exits at once under a service manager")
		fmt.Fprintln(stderr, "example: ggd install-service -- --http 127.0.0.1:8765 --token <redacted>")
		return 1
	}
	command, err := daemonCommand(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Bake the installing directory into the command: service managers
	// start with an unrelated working directory (e.g. /), and the
	// scheduler only fires jobs bound to the daemon's workspace. A user's
	// explicit --cwd still wins because it comes later.
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "resolve working directory:", err)
		return 1
	}
	command = append([]string{command[0], "--cwd", cwd}, command[1:]...)
	var content string
	switch runtime.GOOS {
	case "linux":
		content = systemdUnit(command)
	case "darwin":
		content = launchdPlist(command, filepath.Join(home, ".gg", "ggd.log"))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(stderr, "create service dir:", err)
		return 1
	}
	// The unit may embed a bearer token; keep it owner-only.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		fmt.Fprintln(stderr, "write service file:", err)
		return 1
	}
	fmt.Fprintln(stdout, "wrote", path)
	switch runtime.GOOS {
	case "linux":
		fmt.Fprintln(stdout, "enable and start with:")
		fmt.Fprintln(stdout, "  systemctl --user daemon-reload && systemctl --user enable --now "+systemdUnitName)
	case "darwin":
		fmt.Fprintln(stdout, "load with:")
		fmt.Fprintln(stdout, "  launchctl load "+path)
	}
	return 0
}

func uninstallService(stdout, stderr io.Writer, home string) int {
	path, err := serviceFile(home)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Disable/unload before removing the definition: afterwards the
	// manager can no longer resolve the unit, so `disable` would fail and
	// leave the enablement symlink behind. Best effort: a missing bus or
	// a service that was never enabled is not fatal.
	switch runtime.GOOS {
	case "linux":
		if err := exec.Command("systemctl", "--user", "disable", "--now", systemdUnitName).Run(); err != nil {
			fmt.Fprintln(stderr, "warning: systemctl --user disable --now:", err)
		}
	case "darwin":
		if err := exec.Command("launchctl", "unload", path).Run(); err != nil {
			fmt.Fprintln(stderr, "warning: launchctl unload:", err)
		}
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(stdout, "no service installed at", path)
			return 0
		}
		fmt.Fprintln(stderr, "remove service file:", err)
		return 1
	}
	fmt.Fprintln(stdout, "removed", path)
	return 0
}

// daemonStatus reports whether a ggd process appears to be running.
func daemonStatus(stdout io.Writer, home string) int {
	if DaemonAlive(home) {
		fmt.Fprintln(stdout, "ggd is running")
		return 0
	}
	fmt.Fprintln(stdout, "ggd is not running")
	return 1
}

// resolveHome mirrors config's home resolution for the service subcommands,
// which run before the full config is loaded.
func resolveHome(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}
