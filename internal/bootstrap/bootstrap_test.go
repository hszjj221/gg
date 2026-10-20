package bootstrap

import (
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
)

func TestProviderFactoryUsesOverride(t *testing.T) {
	called := false
	override := func(config.Config) agent.Provider { called = true; return nil }
	f := ProviderFactory(override)
	f(config.Config{})
	if !called {
		t.Fatal("override was not used")
	}
}

func TestProviderFactoryDefaultsToProviderNew(t *testing.T) {
	if ProviderFactory(nil) == nil {
		t.Fatal("default factory is nil")
	}
}

func TestLoadSkillsDisabled(t *testing.T) {
	set, err := LoadSkills(t.TempDir(), t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Available()) != 0 {
		t.Fatalf("disabled skills = %d", len(set.Available()))
	}
}

var _ app.ProviderFactory = ProviderFactory(nil)
