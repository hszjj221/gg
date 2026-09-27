package scheduler

import (
	"testing"
	"time"
)

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
