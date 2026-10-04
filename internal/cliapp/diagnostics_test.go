package cliapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

func TestCLIDebugDiagnosticsGoToConfiguredStderr(t *testing.T) {
	t.Setenv("GG_LOG_LEVEL", "debug")
	var stdout, stderr strings.Builder
	opts := jobTestOptions(t.TempDir(), &stdout, &stderr)
	opts.ProviderFactory = func(config.Config) agent.Provider { return lifecycleProvider{} }
	if code := Run(context.Background(), []string{"--no-skills", "--no-memory", "-p", "private prompt"}, opts); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "private prompt") || strings.Contains(stdout.String(), "runID") {
		t.Fatal("prompt or diagnostics crossed output boundaries")
	}
	var runID string
	seenRequest := false
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] == "conversation turn started" {
			runID, _ = record["runID"].(string)
		}
		if record["msg"] == "model request finished" {
			seenRequest = true
			if record["runID"] != runID || runID == "" {
				t.Fatalf("uncorrelated CLI request: %+v", record)
			}
		}
	}
	if !seenRequest || strings.TrimSpace(stdout.String()) != "done" {
		t.Fatalf("missing result/diagnostics: %s / %s", stdout.String(), stderr.String())
	}
}
