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
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
	"github.com/hszjj221/gg/internal/standingauth"
)

type fakeDigestSource struct{}

func (fakeDigestSource) EmailSummary(context.Context, time.Time) (string, error) {
	return "From: alex@example.com; Subject: Planning notes", nil
}

func (fakeDigestSource) CalendarReminder(context.Context, time.Time) (string, error) {
	return "Today's agenda: 10:30 Planning", nil
}

func TestProactiveBrakes(t *testing.T) {
	for _, command := range []string{"demo", "digest"} {
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
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				home := filepath.Join(dir, "home")
				// A real matching grant must not change the isolated-store denial.
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
					ProviderFactory: func(config.Config) agent.Provider { t.Fatal("proactive command called model provider"); return nil },
					DigestSource:    fakeDigestSource{},
				}
				argv := append([]string{"--profile", "personal", "proactive", command}, tc.flags...)
				if code := Run(context.Background(), argv, opts); code != 0 || stderr.Len() != 0 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				for _, want := range []string{
					"email_summary: " + tc.want, "calendar_reminder: " + tc.want,
					"save Gmail draft (not sent)", "send email to demo@example.com", "Brakes held:",
				} {
					if !strings.Contains(stdout.String(), want) {
						t.Errorf("missing %q in %s", want, stdout.String())
					}
				}
				if command == "demo" {
					for _, want := range []string{"OS notify off (max 0/run)", "Brakes held: digests stayed local", "no Gmail send/draft, calendar_create, memory_add, or notify executed"} {
						if !strings.Contains(stdout.String(), want) {
							t.Errorf("missing %q in %s", want, stdout.String())
						}
					}
				}
				if got := strings.Count(stdout.String(), "denied without grant/human; brakes held."); got != 2 {
					t.Fatalf("draft/send denials = %d: %s", got, stdout.String())
				}
				if command == "digest" && !strings.Contains(stdout.String(), "local only; no model; no outbound") {
					t.Fatalf("missing local-only outcome: %s", stdout.String())
				}
				inbox, agenda := "Fake inbox:", "Fake agenda:"
				if command == "digest" {
					inbox, _ = (fakeDigestSource{}).EmailSummary(context.Background(), time.Time{})
					agenda, _ = (fakeDigestSource{}).CalendarReminder(context.Background(), time.Time{})
				}
				// Only Summarize may surface payloads; Quiet and Note-only hide them.
				if strings.Contains(stdout.String(), inbox) != (tc.want == "Summarize") ||
					strings.Contains(stdout.String(), agenda) != (tc.want == "Summarize") {
					t.Fatalf("unexpected payload visibility: %s", stdout.String())
				}
				notePaths, err := filepath.Glob(filepath.Join(home, ".gg", "proactive", command+"-*", "*.txt"))
				wantFiles := tc.notes || (command == "digest" && tc.want == "Summarize")
				if err != nil || (wantFiles && len(notePaths) != 2) || (!wantFiles && len(notePaths) != 0) {
					t.Fatalf("note paths = %v, err=%v", notePaths, err)
				}
				for _, path := range notePaths {
					data, err := os.ReadFile(path)
					if err != nil || (!strings.Contains(string(data), inbox) && !strings.Contains(string(data), agenda)) {
						t.Fatalf("local candidate %s: %q %v", path, data, err)
					}
				}
				// No session, workspace registration, durable memory, real-grant audit,
				// or connector state is written; only the seeded grant and local files.
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
}

func TestProactiveDigestRequiresGoogle(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		scopes     []string
		flags      []string
	}{
		{name: "disconnected", want: "google not connected"},
		{name: "disconnected during quiet", want: "google not connected", flags: []string{"--quiet-hours", "22:00-07:00", "--at", "23:00"}},
		{name: "missing Gmail scope", scopes: []string{google.ScopeCalendar}, want: google.ScopeGmailRead},
		{name: "missing Calendar scope", scopes: []string{google.ScopeGmailRead}, want: "calendar.events.readonly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			if tc.scopes != nil {
				store, err := connector.Open(filepath.Join(home, ".gg", "connectors"))
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Save(google.Name, connector.Token{AccessToken: "fake", RefreshToken: "fake", Scopes: tc.scopes}); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr strings.Builder
			opts := Options{CWD: dir, HomeDir: home, Stdout: &stdout, Stderr: &stderr,
				ProviderFactory: func(config.Config) agent.Provider { t.Fatal("digest constructed provider"); return nil },
			}
			argv := append([]string{"--profile", "personal", "proactive", "digest"}, tc.flags...)
			if code := Run(context.Background(), argv, opts); code != 1 || !strings.Contains(stderr.String(), tc.want) || !strings.Contains(stderr.String(), "gg connect google") {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed connection emitted digest: %s", stdout.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".gg", "proactive")); !os.IsNotExist(err) {
				t.Fatalf("failed connection wrote digest state: %v", err)
			}
		})
	}
}

func TestProactiveDisabledAndInvalidArgs(t *testing.T) {
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
		{[]string{"proactive", "digest"}, 0},
		{[]string{"--profile", "coding", "proactive", "digest", "--remember-later"}, 0},
		{[]string{"--profile", "personal", "proactive", "digest", "extra"}, 2},
		{[]string{"--profile", "personal", "proactive", "digest", "--quiet-hours", "invalid"}, 2},
		{[]string{"--profile", "personal", "proactive", "digest", "--at", "24:00"}, 2},
		{[]string{"--profile", "personal", "proactive", "digest", "--notify"}, 2},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			var stdout, stderr strings.Builder
			if code := Run(context.Background(), tc.argv, Options{CWD: dir, HomeDir: home, Stdout: &stdout, Stderr: &stderr}); code != tc.code {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, tc.code, stdout.String(), stderr.String())
			}
			if tc.code == 0 && !strings.Contains(stdout.String(), "Quiet: proactivity disabled") {
				t.Fatalf("missing disabled decision: %s", stdout.String())
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("disabled/invalid demo touched home: %v", err)
			}
		})
	}
}
