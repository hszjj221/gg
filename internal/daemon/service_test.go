package daemon

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestInstallUninstallService(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("service installation only supports linux/darwin")
	}
	home := t.TempDir()
	var out, errOut bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errOut, HomeDir: home}

	code := Run(context.Background(), []string{"install-service", "--", "--http", "127.0.0.1:8765", "--token", "secret token"}, opts)
	if code != 0 {
		t.Fatalf("install-service = %d, stderr: %s", code, errOut.String())
	}
	path, err := serviceFile(home)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// The token-bearing unit must be owner-only.
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("service file mode = %v, want 0600", info.Mode())
	}
	if !strings.Contains(content, "--http") || !strings.Contains(content, "127.0.0.1:8765") {
		t.Errorf("unit does not bake daemon flags:\n%s", content)
	}
	if !strings.Contains(content, "secret token") {
		t.Errorf("unit lost the token arg:\n%s", content)
	}
	switch runtime.GOOS {
	case "linux":
		if !strings.Contains(content, "Restart=always") {
			t.Errorf("systemd unit has no Restart=always:\n%s", content)
		}
		if !strings.Contains(out.String(), "systemctl --user") {
			t.Errorf("missing systemctl instructions: %s", out.String())
		}
	case "darwin":
		if !strings.Contains(content, "<key>KeepAlive</key><true/>") {
			t.Errorf("plist has no KeepAlive:\n%s", content)
		}
	}

	out.Reset()
	if code := Run(context.Background(), []string{"uninstall-service"}, opts); code != 0 {
		t.Fatalf("uninstall-service = %d", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("service file still exists after uninstall")
	}
	// Uninstalling again is not an error.
	if code := Run(context.Background(), []string{"uninstall-service"}, opts); code != 0 {
		t.Fatalf("second uninstall-service = %d", code)
	}
}

func TestServiceUnitQuoting(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd quoting test is linux-specific")
	}
	unit := systemdUnit([]string{"/usr/bin/ggd", "--token", "a b'c"})
	execLine := ""
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			execLine = line
		}
	}
	if !strings.Contains(execLine, "'a b'\\''c'") {
		t.Errorf("ExecStart not safely quoted: %s", execLine)
	}
}

func TestInstallServiceRequiresHTTP(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("service installation only supports linux/darwin")
	}
	home := t.TempDir()
	var out, errOut bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errOut, HomeDir: home}

	// Without --http the daemon would enter stdio mode and exit at once
	// under a service manager; the installer must refuse.
	if code := Run(context.Background(), []string{"install-service"}, opts); code == 0 {
		t.Error("install-service without --http = 0, want non-zero")
	}
	if !strings.Contains(errOut.String(), "--http") {
		t.Errorf("stderr does not explain the --http requirement: %q", errOut.String())
	}

	// With --http the install succeeds and bakes the installing directory
	// as --cwd so workspace-bound jobs keep firing.
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"install-service", "--", "--http", "127.0.0.1:8765"}, opts); code != 0 {
		t.Fatalf("install-service = %d, stderr: %s", code, errOut.String())
	}
	path, err := serviceFile(home)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "--cwd") || !strings.Contains(content, cwd) {
		t.Errorf("service definition does not bake --cwd %q:\n%s", cwd, content)
	}
	// A user-supplied --cwd must win over the baked one (last flag wins).
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"install-service", "--", "--http", "127.0.0.1:8765", "--cwd", "/tmp/custom"}, opts); code != 0 {
		t.Fatalf("install-service = %d, stderr: %s", code, errOut.String())
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/tmp/custom") {
		t.Errorf("explicit --cwd lost:\n%s", string(data))
	}
}

func TestDaemonStatus(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	opts := Options{Stdout: &out, HomeDir: home}
	// No pid file: not running.
	if code := Run(context.Background(), []string{"status"}, opts); code == 0 {
		t.Error("status = 0 with no daemon, want non-zero")
	}
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("status output = %q", out.String())
	}
	// A daemon holding the pidfile lock: alive.
	cleanup, err := WritePidFile(home)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out.Reset()
	if code := Run(context.Background(), []string{"status"}, opts); code != 0 {
		t.Error("status = non-zero with lock held, want 0")
	}
	if !strings.Contains(out.String(), "is running") {
		t.Errorf("status output = %q", out.String())
	}
}
