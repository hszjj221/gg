package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/scheduler"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/workspace"
)

func TestDaemonJobExecutorProfile(t *testing.T) {
	for _, tc := range []struct {
		name, parent, profile, want string
	}{
		{"legacy coding", "coding", "", "coding"},
		{"legacy personal", "personal", "", "personal"},
		{"personal override", "coding", "personal", "personal"},
		{"coding override", "personal", "coding", "coding"},
		{"invalid", "coding", "garbage", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			reg, err := workspace.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			provider := &jobProfileProvider{}
			var profiles []string
			rt, err := app.NewRuntime(app.RuntimeOptions{
				Config: config.Config{CWD: root, Profile: tc.parent, Selection: "test:model", NoContextFiles: true},
				ProviderFactory: func(cfg config.Config) agent.Provider {
					profiles = append(profiles, cfg.Profile)
					return provider
				},
				WorkspaceRegistry: reg,
				NoSkills:          true,
				Repository:        session.NewFileRepository(filepath.Join(root, "sessions")),
				Log:               testStdinLogger(),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close(context.Background())
			executor := &daemonJobExecutor{rt: rt, logger: testStdinLogger()}
			summary, path, err := executor.Execute(context.Background(), scheduler.Job{Name: "profile", Profile: tc.profile, Prompt: "say hi"})
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "profile must be coding or personal") || len(profiles) != 0 {
					t.Fatalf("invalid profile: err=%v, provider profiles=%q", err, profiles)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if summary != "done" || path == "" {
				t.Fatalf("summary/path = %q/%q", summary, path)
			}
			if len(profiles) != 1 || profiles[0] != tc.want {
				t.Fatalf("provider profiles = %q, want [%s]", profiles, tc.want)
			}
			identity := "You are gg, a coding agent"
			if tc.want == config.ProfilePersonal {
				identity = "You are gg, the user's personal assistant"
				for _, tool := range provider.request.Tools {
					switch tool.Name {
					case "image_generate", "tts", "stt", "computer_info", "process_list", "process_kill", "open", "notify", "clipboard_read", "clipboard_write":
						t.Errorf("personal job advertised disabled tool %q", tool.Name)
					}
					if strings.HasPrefix(tool.Name, "mcp_") {
						t.Errorf("personal job advertised disabled MCP tool %q", tool.Name)
					}
				}
			}
			if len(provider.request.Messages) == 0 || !strings.Contains(provider.request.Messages[0].Content, identity) {
				t.Fatalf("scheduled turn missing identity %q", identity)
			}
			// A later legacy job must still inherit the process configuration.
			if _, _, err := executor.Execute(context.Background(), scheduler.Job{Name: "legacy", Prompt: "say hi"}); err != nil {
				t.Fatal(err)
			}
			if len(profiles) != 2 || profiles[1] != tc.parent {
				t.Fatalf("next job profiles = %q, want second profile %q", profiles, tc.parent)
			}
		})
	}
}

type jobProfileProvider struct {
	request agent.Request
}

func (p *jobProfileProvider) Complete(_ context.Context, req agent.Request, _ func(agent.Event)) (agent.AssistantMessage, error) {
	p.request = req
	return agent.AssistantMessage{
		Message: agent.Message{Role: agent.RoleAssistant, Content: "done"}, StopReason: agent.StopReasonEndTurn,
	}, nil
}

func TestSanitizeJobName(t *testing.T) {
	cases := map[string]string{
		"morning-brief":   "morning-brief",
		"早间简报":            "早间简报",
		"weird / name: 1": "weird-name-1",
		"":                "job",
		"a":               "a",
	}
	for in, want := range cases {
		if got := sanitizeJobName(in); got != want {
			t.Errorf("sanitizeJobName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeJobName("x"); len(got) > 32 {
		t.Errorf("name too long: %q", got)
	}
	long := "abcdefghijklmnopqrstuvwxyz-0123456789-extra"
	if got := sanitizeJobName(long); len(got) != 32 {
		t.Errorf("long name not truncated to 32: %q", got)
	}
}

func TestPidFileLifecycle(t *testing.T) {
	home := t.TempDir()
	if DaemonAlive(home) {
		t.Fatal("daemon should not be alive before pid file exists")
	}
	cleanup, err := WritePidFile(home)
	if err != nil {
		t.Fatal(err)
	}
	if !DaemonAlive(home) {
		t.Fatal("daemon should be alive after writing pid file")
	}
	cleanup()
	if DaemonAlive(home) {
		t.Fatal("daemon should not be alive after cleanup")
	}
	// A stale pid file for a dead process must not read as alive.
	if err := writePidFileForTest(t, home); err != nil {
		t.Fatal(err)
	}
	if DaemonAlive(home) {
		t.Fatal("stale pid file must not read as alive")
	}
}

func TestWritePidFileEmptyHome(t *testing.T) {
	if _, err := WritePidFile(""); err == nil {
		t.Fatal("expected error for empty home")
	}
}

func writePidFileForTest(t *testing.T, home string) error {
	t.Helper()
	// A stale pid file with no lock holder: 2^31-1 is not a live pid on any
	// real system, and nothing holds the lock on this fresh file.
	dir := filepath.Join(home, ".gg")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "ggd.pid"), []byte("2147483647\n"), 0o644)
}

func TestResolveJobWorkspaceRef(t *testing.T) {
	home := t.TempDir()
	legacyDir := t.TempDir()
	reg, err := workspace.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := reg.Add("legacy", legacyDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		job  scheduler.Job
		want string
	}{
		// WorkspaceID wins over the legacy dir.
		{"id wins", scheduler.Job{WorkspaceID: "w_explicit", Workspace: legacyDir}, "w_explicit"},
		{"id only", scheduler.Job{WorkspaceID: "w_explicit"}, "w_explicit"},
		// Legacy dir resolves through the registry.
		{"legacy dir", scheduler.Job{Workspace: legacyDir}, ws.ID},
		// Unknown legacy dir falls back to default.
		{"unknown dir", scheduler.Job{Workspace: t.TempDir()}, ""},
		// Pre-P2 jobs (no binding at all) land in the default workspace.
		{"no binding", scheduler.Job{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveJobWorkspaceRef(tc.job, reg); got != tc.want {
				t.Fatalf("resolveJobWorkspaceRef(%+v) = %q, want %q", tc.job, got, tc.want)
			}
		})
	}
}
