package cliapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/standingauth"
)

func TestProactiveDemoBrakes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  string
		notes bool
	}{
		{"local digest", nil, "Summarize", false},
		{"quiet hours", []string{"--quiet-hours", "22:00-07:00", "--at", "23:00"}, "Quiet", false},
		{"outside quiet hours", []string{"--quiet-hours", "22:00-07:00", "--at", "07:00"}, "Summarize", false},
		{"capture during quiet hours", []string{"--quiet-hours", "22:00-07:00", "--at", "23:00", "--remember-later"}, "Note-only", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			// A real matching grant must not change the demo's empty-store denial.
			store := standingauth.New(standingauth.DefaultDir(home))
			grant, err := store.Add(standingauth.Grant{
				Subject: "gg on behalf of demo@example.com", ExpiresAt: time.Now().Add(time.Hour),
				SourceQuote: "For one hour gg may send email or create drafts to demo@example.com on my behalf.",
				Scope:       standingauth.Scope{Tools: []string{"gmail_send"}, Consequences: []string{"E", "U"}, Arguments: map[string]json.RawMessage{"to": json.RawMessage(`"demo@example.com"`)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr strings.Builder
			opts := Options{
				CWD: dir, HomeDir: home, Stdout: &stdout, Stderr: &stderr,
				Stdin: strings.NewReader(""), IsTerminal: func(any) bool { return false },
				ProviderFactory: func(config.Config) agent.Provider { t.Fatal("demo called model provider"); return nil },
			}
			argv := append([]string{"--profile", "personal", "proactive", "demo"}, tc.flags...)
			if code := Run(context.Background(), argv, opts); code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			for _, want := range []string{
				"email_summary: " + tc.want, "calendar_reminder: " + tc.want,
				"OS notify off (max 0/run)", "save Gmail draft (not sent)", "send email to demo@example.com",
				"Brakes held: digests stayed local", "no Gmail send/draft, calendar_create, memory_add, or notify executed",
			} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("missing %q in %s", want, stdout.String())
				}
			}
			if got := strings.Count(stdout.String(), "denied without grant/human; brakes held."); got != 2 {
				t.Fatalf("draft/send denials = %d: %s", got, stdout.String())
			}
			// Only Summarize may surface payloads; Quiet and Note-only hide them.
			if strings.Contains(stdout.String(), "Fake inbox:") != (tc.want == "Summarize") ||
				strings.Contains(stdout.String(), "Fake agenda:") != (tc.want == "Summarize") {
				t.Fatalf("unexpected payload visibility: %s", stdout.String())
			}
			notePaths, err := filepath.Glob(filepath.Join(home, ".gg", "proactive", "demo-*", "*.txt"))
			if err != nil || (tc.notes && len(notePaths) != 2) || (!tc.notes && len(notePaths) != 0) {
				t.Fatalf("note paths = %v, err=%v", notePaths, err)
			}
			for _, path := range notePaths {
				data, err := os.ReadFile(path)
				if err != nil || !strings.HasPrefix(string(data), "Fake ") {
					t.Fatalf("local candidate %s: %q %v", path, data, err)
				}
			}
			// No session, workspace registration, durable memory, real-grant audit,
			// or connector state is written; only the seeded grant and demo notes.
			if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && path != filepath.Join(standingauth.DefaultDir(home), grant.ID+".json") && !strings.HasSuffix(path, ".txt") {
					t.Errorf("unexpected state written: %s", path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProactiveDemoDisabledAndInvalidArgs(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		code int
	}{
		{[]string{"proactive", "demo"}, 0},
		{[]string{"--profile", "coding", "proactive", "demo", "--remember-later"}, 0},
		{[]string{"--profile", "personal", "proactive"}, 2},
		{[]string{"--profile", "personal", "proactive", "demo", "extra"}, 2},
		{[]string{"--profile", "personal", "proactive", "demo", "--quiet-hours", "invalid"}, 2},
		{[]string{"--profile", "personal", "proactive", "demo", "--at", "24:00"}, 2},
		{[]string{"--profile", "personal", "proactive", "demo", "--notify"}, 2},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			var stdout, stderr strings.Builder
			if code := Run(context.Background(), tc.argv, Options{CWD: dir, HomeDir: home, Stdout: &stdout, Stderr: &stderr}); code != tc.code {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, tc.code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("disabled/invalid demo touched home: %v", err)
			}
		})
	}
}
