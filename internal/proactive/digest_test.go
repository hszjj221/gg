package proactive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

func TestRunDigest(t *testing.T) {
	for _, tc := range []struct {
		name, profile, quietHours string
		later                     bool
		want                      Decision
		files                     int
	}{
		{"summarize", "personal", "", false, Summarize, 2},
		{"quiet", "personal", "22:00-07:00", false, Quiet, 0},
		{"note during quiet", "personal", "22:00-07:00", true, NoteOnly, 2},
		{"coding", "coding", "", false, Quiet, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			var out strings.Builder
			result, err := RunDigest(context.Background(), DigestOptions{
				Profile: tc.profile, QuietHours: tc.quietHours, RememberLater: tc.later,
				HomeDir: home, Now: time.Date(2026, 10, 25, 23, 0, 0, 0, time.Local), Writer: &out,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.profile == "coding" {
				if result.Directory != "" || !strings.Contains(result.Summary, "Quiet: proactivity disabled") {
					t.Fatalf("coding result = %+v", result)
				}
				if _, err := os.Stat(home); !os.IsNotExist(err) {
					t.Fatalf("coding touched home: %v", err)
				}
				return
			}
			for _, scenario := range []Scenario{EmailSummary, CalendarReminder} {
				if !strings.Contains(out.String(), string(scenario)+": "+string(tc.want)) {
					t.Fatalf("missing decision: %s", out.String())
				}
			}
			if strings.Contains(out.String(), "Stub inbox:") != (tc.want == Summarize) ||
				strings.Contains(out.String(), "Stub agenda:") != (tc.want == Summarize) ||
				strings.Contains(result.Summary, "Stub ") {
				t.Fatalf("unexpected payload visibility: stdout=%s summary=%s", out.String(), result.Summary)
			}
			if strings.Count(out.String(), "denied without grant/human; brakes held.") != 2 {
				t.Fatalf("missing draft/send denials: %s", out.String())
			}
			info, err := os.Stat(result.Directory)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("digest directory permissions: %v %v", info, err)
			}
			entries, err := os.ReadDir(result.Directory)
			if err != nil || len(entries) != tc.files {
				t.Fatalf("digest files = %v, err=%v", entries, err)
			}
			for _, entry := range entries {
				path := filepath.Join(result.Directory, entry.Name())
				info, err := entry.Info()
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("file permissions: %v %v", info, err)
				}
				data, err := os.ReadFile(path)
				if err != nil || !strings.HasPrefix(string(data), "Stub ") {
					t.Fatalf("digest %s = %q, err=%v", path, data, err)
				}
			}
		})
	}
}

func TestRunDigestWallClockAndUniqueDirectory(t *testing.T) {
	opts := DigestOptions{Profile: "personal", HomeDir: t.TempDir()}
	before := time.Now().Add(30 * time.Minute).Format("15:04")
	first, err := RunDigest(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().Add(30 * time.Minute).Format("15:04")
	data, err := os.ReadFile(filepath.Join(first.Directory, "calendar_reminder.txt"))
	if err != nil || (!strings.Contains(string(data), before) && !strings.Contains(string(data), after)) {
		t.Fatalf("wall-clock agenda = %q, err=%v", data, err)
	}
	second, err := RunDigest(context.Background(), opts)
	if err != nil || first.Directory == second.Directory {
		t.Fatalf("digest directories reused: %q/%q, err=%v", first.Directory, second.Directory, err)
	}
}

func TestDigestBrakesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  Decision
		allowOn int
		err     error
	}{
		{"draft allowed", Quiet, 1, nil},
		{"send allowed", Quiet, 2, nil},
		{"policy surfaced action", Summarize, 0, nil},
		{"approval error", Quiet, 0, errors.New("approval unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			approver := digestApproverFunc(func(context.Context, agent.ApprovalRequest) (agent.ApprovalDecision, error) {
				calls++
				return agent.ApprovalDecision{Allow: calls == tc.allowOn}, tc.err
			})
			err := checkDigestBrakes(context.Background(), func(Input) Result { return Result{Decision: tc.policy} }, approver, time.Now(), io.Discard)
			if err == nil || !strings.Contains(err.Error(), "brakes failed:") {
				t.Fatalf("brake error = %v", err)
			}
		})
	}
}

type digestApproverFunc func(context.Context, agent.ApprovalRequest) (agent.ApprovalDecision, error)

func (f digestApproverFunc) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return f(ctx, req)
}
