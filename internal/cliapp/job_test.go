package cliapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/scheduler"
)

func jobTestOptions(dir string, stdout, stderr *strings.Builder) Options {
	return Options{
		CWD:     dir,
		HomeDir: filepath.Join(dir, "home"),
		Stdout:  stdout,
		Stderr:  stderr,
		ProviderFactory: func(config.Config) agent.Provider {
			return &appFakeProvider{}
		},
	}
}

func TestJobAddListShowRemove(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	code := Run(context.Background(), []string{"job", "add", "--in", "30m", "--name", "drink", "remind me to drink water"}, opts)
	if code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	code = Run(context.Background(), []string{"job", "list"}, opts)
	if code != 0 {
		t.Fatalf("job list exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "drink") {
		t.Fatalf("job list missing job: %q", stdout.String())
	}

	// Extract the id from the add output is brittle; find by name via show.
	stdout.Reset()
	code = Run(context.Background(), []string{"job", "show", "drink"}, opts)
	if code != 0 {
		t.Fatalf("job show exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "remind me to drink water") {
		t.Fatalf("job show missing prompt: %q", stdout.String())
	}

	stdout.Reset()
	code = Run(context.Background(), []string{"job", "pause", "drink"}, opts)
	if code != 0 {
		t.Fatalf("job pause exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	code = Run(context.Background(), []string{"job", "list"}, opts)
	if code != 0 || strings.Contains(stdout.String(), "drink") {
		t.Fatalf("paused job should be hidden from default list: %q", stdout.String())
	}
	stdout.Reset()
	code = Run(context.Background(), []string{"job", "list", "--all"}, opts)
	if code != 0 || !strings.Contains(stdout.String(), "paused") {
		t.Fatalf("paused job should show with --all: %q", stdout.String())
	}
	stdout.Reset()
	code = Run(context.Background(), []string{"job", "resume", "drink"}, opts)
	if code != 0 {
		t.Fatalf("job resume exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	code = Run(context.Background(), []string{"job", "remove", "drink"}, opts)
	if code != 0 {
		t.Fatalf("job remove exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	code = Run(context.Background(), []string{"job", "list", "--all"}, opts)
	if code != 0 || strings.Contains(stdout.String(), "drink") {
		t.Fatalf("removed job should be gone: %q", stdout.String())
	}
}

func TestJobAddValidation(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"bad cron", []string{"job", "add", "--cron", "bogus", "--name", "x", "prompt"}, 1},
		{"no schedule", []string{"job", "add", "--name", "x", "prompt"}, 2},
		{"two schedules", []string{"job", "add", "--cron", "* * * * *", "--in", "5m", "--name", "x", "prompt"}, 2},
		{"no prompt", []string{"job", "add", "--in", "5m", "--name", "x"}, 2},
		{"bad at", []string{"job", "add", "--at", "someday", "--name", "x", "prompt"}, 1},
		{"unknown job", []string{"job", "show", "nope"}, 1},
	}
	for _, c := range cases {
		var stdout, stderr strings.Builder
		if code := Run(context.Background(), c.args, jobTestOptions(dir, &stdout, &stderr)); code != c.code {
			t.Errorf("%s: exit %d, want %d (stderr: %s)", c.name, code, c.code, stderr.String())
		}
	}
}

func TestJobAddCronAndAt(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	if code := Run(context.Background(), []string{"job", "add", "--cron", "0 9 * * *", "--name", "m", "morning"}, opts); code != 0 {
		t.Fatalf("cron add exit %d: %s", code, stderr.String())
	}
	if code := Run(context.Background(), []string{"job", "add", "--at", "2030-01-01 09:00", "--name", "o", "once"}, opts); code != 0 {
		t.Fatalf("at add exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"job", "list"}, opts); code != 0 {
		t.Fatalf("list exit %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "0 9 * * *") || !strings.Contains(out, "once") {
		t.Fatalf("list missing jobs: %q", out)
	}
}

func TestJobLogFiltersBeforeLimit(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	for _, name := range []string{"aaa", "bbb"} {
		if code := Run(context.Background(), []string{"job", "add", "--in", "30m", "--name", name, "prompt " + name}, opts); code != 0 {
			t.Fatalf("job add %s exit %d: %s", name, code, stderr.String())
		}
	}
	// Write 5 records for bbb (newest) and 1 older record for aaa directly.
	cfg, err := resolveJobTestConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	store, err := scheduler.Open(cfg.Scheduler.Dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]scheduler.Job{}
	for _, j := range jobs {
		byName[j.Name] = j
	}
	base := time.Now().Add(-time.Hour)
	if err := store.AppendRun(scheduler.RunRecord{JobID: byName["aaa"].ID, JobName: "aaa", StartedAt: base, FinishedAt: base, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i+1) * time.Minute)
		if err := store.AppendRun(scheduler.RunRecord{JobID: byName["bbb"].ID, JobName: "bbb", StartedAt: ts, FinishedAt: ts, Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}

	// --limit 3 alone would only see bbb's records; filtering first must
	// still find aaa's older record.
	stdout.Reset()
	if code := Run(context.Background(), []string{"job", "log", "--job", "aaa", "--limit", "3"}, opts); code != 0 {
		t.Fatalf("job log exit %d: %s", code, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "aaa") {
		t.Fatalf("job log --job aaa --limit 3 missing aaa's record: %q", out)
	}
}

func TestJobLogRemovedJob(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	if code := Run(context.Background(), []string{"job", "add", "--in", "30m", "--name", "gone", "prompt"}, opts); code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	cfg, err := resolveJobTestConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	store, err := scheduler.Open(cfg.Scheduler.Dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.List()
	id := jobs[0].ID
	ts := time.Now()
	if err := store.AppendRun(scheduler.RunRecord{JobID: id, JobName: "gone", StartedAt: ts, FinishedAt: ts, Status: "ok", Summary: "did it"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"job", "remove", "gone"}, opts); code != 0 {
		t.Fatalf("job remove exit %d: %s", code, stderr.String())
	}
	// The audit log must stay accessible by id and by name after removal.
	for _, ref := range []string{id, "gone"} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(context.Background(), []string{"job", "log", "--job", ref}, opts); code != 0 {
			t.Fatalf("job log --job %s exit %d: %s", ref, code, stderr.String())
		}
		if out := stdout.String(); !strings.Contains(out, "did it") {
			t.Fatalf("job log --job %s missing removed job's record: %q", ref, out)
		}
	}
}

func TestJobResumePastDueOnce(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	// A once job scheduled in the past: Add keeps it, RefreshNext yields nil.
	if code := Run(context.Background(), []string{"job", "add", "--at", "2020-01-01 00:00", "--name", "late", "prompt"}, opts); code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	if code := Run(context.Background(), []string{"job", "pause", "late"}, opts); code != 0 {
		t.Fatalf("job pause exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"job", "resume", "late"}, opts); code != 0 {
		t.Fatalf("job resume exit %d: %s", code, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, "will fire immediately") {
		t.Fatalf("resume of past-due once job should fire immediately: %q", out)
	}
	cfg, err := resolveJobTestConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	store, err := scheduler.Open(cfg.Scheduler.Dir)
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.List()
	if len(jobs) != 1 || jobs[0].NextRun == nil {
		t.Fatalf("resumed once job has no NextRun: %+v", jobs)
	}
}

func TestJobResumeAlreadyRanOnce(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	if code := Run(context.Background(), []string{"job", "add", "--in", "30m", "--name", "done", "prompt"}, opts); code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	cfg, err := resolveJobTestConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	store, err := scheduler.Open(cfg.Scheduler.Dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a completed run, then pause.
	if err := store.Update(func(jobs *[]scheduler.Job) error {
		(*jobs)[0].RunCount = 1
		(*jobs)[0].Enabled = false
		(*jobs)[0].NextRun = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"job", "resume", "done"}, opts); code == 0 {
		t.Fatalf("resume of an already-ran once job should fail, output: %q", stdout.String())
	}
	if out := stderr.String(); !strings.Contains(out, "already ran") {
		t.Fatalf("expected 'already ran' error, got: %q", out)
	}
}

func TestJobAddRecordsWorkspace(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	if code := Run(context.Background(), []string{"job", "add", "--in", "30m", "--name", "w", "prompt"}, opts); code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"job", "show", "w"}, opts); code != 0 {
		t.Fatalf("job show exit %d: %s", code, stderr.String())
	}
	if out := stdout.String(); !strings.Contains(out, dir) {
		t.Fatalf("job show should display the creating workspace %q: %q", dir, out)
	}
}

func TestJobRunAppliesTimeout(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)
	opts.ProviderFactory = func(config.Config) agent.Provider {
		return &blockingProvider{}
	}

	if code := Run(context.Background(), []string{"job", "add", "--in", "30m", "--name", "t", "--timeout", "1s", "say hi"}, opts); code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	start := time.Now()
	if code := Run(context.Background(), []string{"job", "run", "t"}, opts); code == 0 {
		t.Fatalf("blocking job run should fail after the timeout")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("job run took %v, timeout was not applied", elapsed)
	}
	// The run record must be marked as a timeout.
	cfg, err := resolveJobTestConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	store, err := scheduler.Open(cfg.Scheduler.Dir)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := store.ReadRuns(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != "timeout" {
		t.Fatalf("run records = %+v, want one timeout record", recs)
	}
}

func TestJobRunNoSkills(t *testing.T) {
	dir := t.TempDir()
	// A project skill under the CWD must be ignored with --no-skills.
	skillDir := filepath.Join(dir, ".agents", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)
	prov := &skillSpyProvider{}
	opts.ProviderFactory = func(config.Config) agent.Provider { return prov }

	if code := Run(context.Background(), []string{"job", "add", "--in", "30m", "--name", "t", "say hi"}, opts); code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"--no-skills", "job", "run", "t"}, opts); code != 0 {
		t.Fatalf("job run exit %d: %s", code, stderr.String())
	}
	if prov.seenSkills {
		t.Fatal("--no-skills was not honored by `gg job run`")
	}
}

// resolveJobTestConfig re-resolves the test config so tests can open the
// scheduler store directly.
func resolveJobTestConfig(opts Options) (config.Config, error) {
	return config.Resolve(config.Options{
		CWD:     opts.CWD,
		HomeDir: opts.HomeDir,
	})
}

// blockingProvider waits for ctx cancellation, exercising job timeouts.
type blockingProvider struct{}

func (p *blockingProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	<-ctx.Done()
	return agent.AssistantMessage{}, ctx.Err()
}

// skillSpyProvider records whether any skill content reached the request.
type skillSpyProvider struct {
	seenSkills bool
}

func (p *skillSpyProvider) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "demo") {
			p.seenSkills = true
		}
	}
	if onEvent != nil {
		onEvent(agent.Event{Type: agent.EventTextDelta, Text: "hello"})
	}
	return agent.AssistantMessage{
		Message: agent.Message{Role: agent.RoleAssistant, Content: "hello",
			ContentBlocks: []agent.ContentBlock{{Type: agent.ContentText, Text: "hello"}}},
		StopReason: agent.StopReasonEndTurn,
	}, nil
}

func TestJobRunExecutesTurn(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	if code := Run(context.Background(), []string{"job", "add", "--in", "5m", "--name", "t", "say hi"}, opts); code != 0 {
		t.Fatalf("job add exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"job", "run", "t"}, opts); code != 0 {
		t.Fatalf("job run exit %d: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "hello\n" {
		t.Fatalf("job run stdout = %q, want %q", got, "hello\n")
	}
}

func TestJobPromptFallback(t *testing.T) {
	// A prompt starting with "job" but not a subcommand stays a prompt.
	args, err := cli.Parse([]string{"job", "well", "done"})
	if err != nil {
		t.Fatal(err)
	}
	if args.Command != cli.CommandRun || args.Prompt != "job well done" {
		t.Fatalf("got command %q prompt %q", args.Command, args.Prompt)
	}
	args, err = cli.Parse([]string{"job", "add", "--in", "5m", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if args.Command != cli.CommandJob {
		t.Fatalf("got command %q, want job", args.Command)
	}
}
