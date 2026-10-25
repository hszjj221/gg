package scheduler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJobProactiveDigest(t *testing.T) {
	for _, tc := range []struct {
		name, profile, prompt, wantError string
		digest                           bool
	}{
		{"digest personal", "personal", "", "", true},
		{"digest with prompt", "personal", "ignored", "", true},
		{"digest empty profile", "", "", "require profile personal", true},
		{"digest coding", "coding", "p", "require profile personal", true},
		{"agent empty prompt", "personal", "", "job prompt is required", false},
		{"legacy agent", "", "p", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := Job{Name: "digest", Kind: KindCron, Schedule: "0 9 * * *", Profile: tc.profile, Prompt: tc.prompt, ProactiveDigest: tc.digest}
			err := job.Validate()
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("Validate() = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"proactive_digest":true`) != tc.digest || (!tc.digest && strings.Contains(string(raw), "proactive_digest")) {
				t.Fatalf("unexpected JSON: %s", raw)
			}
			var decoded Job
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.ProactiveDigest != tc.digest || decoded.Prompt != tc.prompt {
				t.Fatalf("round trip = %+v", decoded)
			}
		})
	}
}

func TestJobProfile(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		invalid       bool
	}{
		{name: "legacy"},
		{name: "coding", profile: "coding"},
		{name: "personal", profile: "personal"},
		{name: "unknown", profile: "garbage", invalid: true},
		{name: "whitespace", profile: " ", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := Job{Name: "profile", Prompt: "p", Kind: KindCron, Schedule: "0 9 * * *", Profile: tc.profile}
			err := job.Validate()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "profile must be coding or personal") {
					t.Fatalf("Validate() = %v, want invalid profile error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"profile":`) != (tc.profile != "") {
				t.Fatalf("unexpected profile JSON: %s", raw)
			}
			var decoded Job
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Profile != tc.profile {
				t.Fatalf("JSON profile = %q, want %q", decoded.Profile, tc.profile)
			}
			store, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Add(decoded); err != nil {
				t.Fatal(err)
			}
			jobs, err := store.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 1 || jobs[0].Profile != tc.profile {
				t.Fatalf("persisted jobs = %+v, want profile %q", jobs, tc.profile)
			}
		})
	}
}

func TestJobAllowOrdinaryJSONCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"enabled", `{"auto_approve":true}`, true},
		{"disabled", `{"auto_approve":false}`, false},
		{"omitted", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var job Job
			if err := json.Unmarshal([]byte(tc.raw), &job); err != nil {
				t.Fatal(err)
			}
			if job.AllowOrdinary != tc.want {
				t.Fatalf("AllowOrdinary = %v, want %v", job.AllowOrdinary, tc.want)
			}
			encoded, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			wantJSON, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if string(fields["auto_approve"]) != string(wantJSON) {
				t.Fatalf("legacy auto_approve key lost: %s", encoded)
			}
			if _, ok := fields["allow_ordinary"]; ok {
				t.Fatalf("unexpected allow_ordinary JSON key: %s", encoded)
			}
		})
	}
}

func TestParseCronValid(t *testing.T) {
	for _, expr := range []string{"0 9 * * *", "* * * * *", "*/15 8-18 * * 1-5", "@daily", "@hourly"} {
		if _, err := ParseCron(expr); err != nil {
			t.Errorf("ParseCron(%q) = %v, want nil", expr, err)
		}
	}
}

func TestParseCronInvalid(t *testing.T) {
	for _, expr := range []string{"", "not a cron", "0 9 * *", "* * * * * *"} {
		if _, err := ParseCron(expr); err == nil {
			t.Errorf("ParseCron(%q) = nil, want error", expr)
		}
	}
}

func TestJobValidate(t *testing.T) {
	base := Job{Name: "n", Prompt: "p", Kind: KindCron, Schedule: "0 9 * * *", Timezone: "Asia/Shanghai"}
	if err := base.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Job)
	}{
		{"empty name", func(j *Job) { j.Name = "" }},
		{"empty prompt", func(j *Job) { j.Prompt = "" }},
		{"bad cron", func(j *Job) { j.Schedule = "xxx" }},
		{"bad tz", func(j *Job) { j.Timezone = "Mars/Olympus" }},
		{"bad kind", func(j *Job) { j.Kind = "sometimes" }},
		{"bad once", func(j *Job) { j.Kind = KindOnce; j.Schedule = "tomorrow-ish" }},
	}
	for _, c := range cases {
		j := base
		c.mut(&j)
		if err := j.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want error", c.name)
		}
	}
}

func TestParseOnceLayouts(t *testing.T) {
	loc := time.UTC
	for _, spec := range []string{"2026-09-28T09:00:00Z", "2026-09-28 09:00", "2026-09-28"} {
		got, err := ParseOnce(spec, loc)
		if err != nil {
			t.Errorf("ParseOnce(%q) = %v", spec, err)
			continue
		}
		if got.Year() != 2026 || got.Month() != 9 || got.Day() != 28 {
			t.Errorf("ParseOnce(%q) = %v, wrong date", spec, got)
		}
	}
}

func TestCronNextAfter(t *testing.T) {
	j := Job{Kind: KindCron, Schedule: "0 9 * * *", Timezone: "Asia/Shanghai"}
	// 2026-09-28 08:00 +08:00 -> next fire 09:00 same day.
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	next, ok := j.NextAfter(now)
	if !ok {
		t.Fatal("NextAfter = !ok")
	}
	want := time.Date(2026, 9, 28, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if !next.Equal(want) {
		t.Errorf("NextAfter = %v, want %v", next, want)
	}
}

func TestOnceNextAfter(t *testing.T) {
	j := Job{Kind: KindOnce, Schedule: "2026-09-28T09:00:00+08:00", Timezone: "Asia/Shanghai"}
	// Job fires at 01:00 UTC.
	before := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if _, ok := j.NextAfter(before); !ok {
		t.Error("once job before its time: NextAfter = !ok")
	}
	after := time.Date(2026, 9, 28, 2, 0, 0, 0, time.UTC)
	if _, ok := j.NextAfter(after); ok {
		t.Error("once job after its time: NextAfter = ok, want !ok")
	}
}

func TestRefreshNextDisabled(t *testing.T) {
	j := Job{Kind: KindCron, Schedule: "* * * * *", Enabled: false}
	j.RefreshNext(time.Now())
	if j.NextRun != nil {
		t.Error("disabled job should have nil NextRun")
	}
}

func TestJobWorkspaceIDRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(Job{
		Name: "w", Kind: KindCron, Schedule: "* * * * *", Prompt: "p",
		Workspace: "/tmp/legacy", WorkspaceID: "w_abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("listed %d jobs, want 1", len(jobs))
	}
	got := jobs[0]
	if got.WorkspaceID != "w_abc123" {
		t.Fatalf("WorkspaceID = %q, want %q", got.WorkspaceID, "w_abc123")
	}
	// The legacy dir field survives alongside the new ID field.
	if got.Workspace != "/tmp/legacy" {
		t.Fatalf("Workspace = %q, want legacy dir preserved", got.Workspace)
	}
	// JSON shape: workspace_id is omitempty; legacy workspace key unchanged.
	raw, err := json.Marshal(added)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["workspace_id"] != "w_abc123" {
		t.Fatalf("workspace_id missing in %s", raw)
	}
	if m["workspace"] != "/tmp/legacy" {
		t.Fatalf("workspace missing in %s", raw)
	}
}
