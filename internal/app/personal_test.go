package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
)

func testPersonalConfig(home string) config.Config {
	return config.Config{
		Memory: config.MemoryConfig{
			Enabled:               true,
			MaxPromptTokens:       config.DefaultMemoryMaxTokens,
			Dir:                   memory.DefaultDir(home),
			DailyLogTailTokens:    config.DefaultDailyLogTailTokens,
			DailyLogRetentionDays: config.DefaultDailyLogRetentionDays,
		},
		MemoryPath: filepath.Join(home, ".gg", "memory.md"),
		UserFile:   filepath.Join(home, ".gg", "USER.md"),
	}
}

func TestSetupPersonalMigratesLegacyAndLoadsProfile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gg", "memory.md"), []byte("- old fact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gg", "USER.md"), []byte("- Name: Ada\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	personal, notice, err := SetupPersonal(testPersonalConfig(home))
	if err != nil {
		t.Fatalf("SetupPersonal failed: %v", err)
	}
	if !strings.Contains(notice, "migrated") {
		t.Fatalf("expected migration notice, got %q", notice)
	}
	if personal.Profile.Name != "Ada" {
		t.Fatalf("profile not loaded: %+v", personal.Profile)
	}
	data, err := os.ReadFile(filepath.Join(home, ".gg", "memory", "MEMORY.md"))
	if err != nil || !strings.Contains(string(data), "old fact") {
		t.Fatalf("migrated content missing: %q err=%v", data, err)
	}
}

func TestSetupPersonalBrokenProfileDoesNotBlockStartup(t *testing.T) {
	home := t.TempDir()
	// USER.md as a directory: every read fails.
	if err := os.MkdirAll(filepath.Join(home, ".gg", "USER.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	personal, notice, err := SetupPersonal(testPersonalConfig(home))
	if err != nil {
		t.Fatalf("broken profile must not block startup: %v", err)
	}
	if !strings.Contains(notice, "warning") {
		t.Fatalf("expected warning notice, got %q", notice)
	}
	if !personal.Profile.Empty() {
		t.Fatalf("expected zero profile, got %+v", personal.Profile)
	}
	if personal.Store == nil {
		t.Fatal("memory store must still be set up")
	}
}

func TestSetupPersonalMemoryDisabledSkipsStore(t *testing.T) {
	home := t.TempDir()
	cfg := testPersonalConfig(home)
	cfg.Memory.Enabled = false
	personal, _, err := SetupPersonal(cfg)
	if err != nil {
		t.Fatalf("SetupPersonal failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gg", "memory")); !os.IsNotExist(err) {
		t.Fatalf("memory layout must not be created when disabled: %v", err)
	}
	if personal.Store == nil {
		t.Fatal("store must be non-nil even when disabled")
	}
}
