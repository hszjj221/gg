package scheduler

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreAddListRoundTrip(t *testing.T) {
	s := openTestStore(t)
	added, err := s.Add(Job{Name: "morning", Kind: KindCron, Schedule: "0 9 * * *", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == "" || !added.Enabled || added.NextRun == nil {
		t.Errorf("Add defaults wrong: %+v", added)
	}
	// Reopen from the same dir: data must survive.
	s2, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != added.ID || jobs[0].Name != "morning" {
		t.Errorf("List() = %+v", jobs)
	}
}

func TestStoreListDoesNotRewriteJobsFile(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Add(Job{Name: "a", Kind: KindCron, Schedule: "0 9 * * *", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	path := s.Dir() + "/jobs.json"
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A pure read must not rewrite the file.
	for i := 0; i < 3; i++ {
		if _, err := s.List(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("List() rewrote jobs.json")
	}
	if infoAfter, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Error("List() touched jobs.json mtime")
	}
}
func TestStoreAddInvalidRejected(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Add(Job{Name: "x", Kind: KindCron, Schedule: "bogus", Prompt: "p"}); err == nil {
		t.Error("Add(invalid cron) = nil, want error")
	}
	jobs, _ := s.List()
	if len(jobs) != 0 {
		t.Errorf("invalid job was stored: %+v", jobs)
	}
}

func TestStoreConcurrentUpdateNoCorruption(t *testing.T) {
	s := openTestStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.Update(func(jobs *[]Job) error {
				*jobs = append(*jobs, Job{ID: NewID(), Name: "j", Kind: KindCron, Schedule: "* * * * *", Prompt: "p", Enabled: true})
				return nil
			})
		}(i)
	}
	wg.Wait()
	jobs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 20 {
		t.Errorf("got %d jobs, want 20 (lost update under concurrency)", len(jobs))
	}
}

func TestRunLogAppendRead(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 3; i++ {
		if err := s.AppendRun(RunRecord{JobID: "a", Status: "ok", Summary: "s"}); err != nil {
			t.Fatal(err)
		}
	}
	recs, err := s.ReadRuns(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("ReadRuns(2) = %d records", len(recs))
	}
	recs, _ = s.ReadRuns(0)
	if len(recs) != 3 {
		t.Errorf("ReadRuns(0) = %d records, want 3", len(recs))
	}
}

func TestUnattendedApprover(t *testing.T) {
	ctx := t.Context()
	deny := UnattendedApprover{}
	d, err := deny.Approve(ctx, agent.ApprovalRequest{ToolName: "write"})
	if err != nil || d.Allow {
		t.Errorf("default approver = %+v,%v; want deny", d, err)
	}
	allow := UnattendedApprover{AllowAll: true}
	d, err = allow.Approve(ctx, agent.ApprovalRequest{ToolName: "write"})
	if err != nil || !d.Allow {
		t.Errorf("allow-all approver = %+v,%v; want allow", d, err)
	}
}

type fakeExecutor struct {
	mu    sync.Mutex
	calls []Job
	block chan struct{} // if non-nil, Execute blocks until closed
	err   error
}

func (f *fakeExecutor) Execute(ctx context.Context, job Job) (string, string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, job)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	if f.err != nil {
		return "", "", f.err
	}
	return "done: " + job.Name, "/tmp/sess", nil
}

func (f *fakeExecutor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeExecutor) lastJobName() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1].Name
}

// waitForRunCount polls until the job's RunCount reaches n. Tests must not
// return while a dispatched executor goroutine is still writing to the store:
// its O_CREATE lock file can otherwise be recreated mid-RemoveAll and fail
// TempDir cleanup with "directory not empty".
func waitForRunCount(t *testing.T, s *Store, id string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := s.List()
		if err == nil {
			for _, j := range jobs {
				if j.ID == id && j.RunCount >= n {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s RunCount did not reach %d", id, n)
}

func TestSchedulerTickFiresDueJob(t *testing.T) {
	s := openTestStore(t)
	exec := &fakeExecutor{}
	sch := New(s, exec)

	now := time.Now()
	past := now.Add(-time.Second)
	job, err := s.Add(Job{Name: "due", Kind: KindCron, Schedule: "* * * * *", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	// Force the job to be due.
	_ = s.Update(func(jobs *[]Job) error {
		(*jobs)[0].NextRun = &past
		return nil
	})

	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for exec.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if exec.count() != 1 {
		t.Fatalf("executor called %d times, want 1", exec.count())
	}
	// Wait for bookkeeping to land.
	deadline = time.Now().Add(3 * time.Second)
	for {
		jobs, _ := s.List()
		if len(jobs) == 1 && jobs[0].RunCount == 1 && jobs[0].ID == job.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bookkeeping missing: %+v", jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	recs, _ := s.ReadRuns(0)
	if len(recs) != 1 || recs[0].Status != "ok" || recs[0].Summary != "done: due" {
		t.Errorf("run records = %+v", recs)
	}
}

func TestSchedulerSkipsOverlappingRun(t *testing.T) {
	s := openTestStore(t)
	exec := &fakeExecutor{block: make(chan struct{})}
	sch := New(s, exec)

	past := time.Now().Add(-time.Second)
	added, err := s.Add(Job{Name: "slow", Kind: KindCron, Schedule: "* * * * *", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Update(func(jobs *[]Job) error {
		(*jobs)[0].NextRun = &past
		return nil
	})

	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for exec.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// The persisted NextRun must advance before dispatch, so a second tick
	// while the first run is blocked sees nothing due: no second dispatch
	// and no "skipped" spam.
	jobs, _ := s.List()
	if jobs[0].NextRun == nil || !jobs[0].NextRun.After(time.Now()) {
		t.Fatalf("NextRun not advanced before dispatch: %+v", jobs[0].NextRun)
	}
	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if exec.count() != 1 {
		t.Fatalf("overlapping run was not skipped: %d calls", exec.count())
	}
	close(exec.block)
	waitForRunCount(t, s, added.ID, 1)
	recs, _ := s.ReadRuns(0)
	for _, r := range recs {
		if r.Status == "skipped" {
			t.Errorf("unexpected skipped record with advance-before-dispatch: %+v", r)
		}
	}
}

func TestSchedulerBusyDueRecordsSingleSkipped(t *testing.T) {
	s := openTestStore(t)
	exec := &fakeExecutor{block: make(chan struct{})}
	sch := New(s, exec)

	past := time.Now().Add(-time.Second)
	added, err := s.Add(Job{Name: "slow", Kind: KindCron, Schedule: "* * * * *", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Update(func(jobs *[]Job) error {
		(*jobs)[0].NextRun = &past
		return nil
	})
	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for exec.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// Force the next occurrence to be due while the first run is still
	// blocked: exactly one skipped record, then NextRun advances so the loop
	// cannot spin.
	_ = s.Update(func(jobs *[]Job) error {
		(*jobs)[0].NextRun = &past
		return nil
	})
	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if exec.count() != 1 {
		t.Fatalf("busy job dispatched %d times, want 1", exec.count())
	}
	recs, _ := s.ReadRuns(0)
	var skipped int
	for _, r := range recs {
		if r.Status == "skipped" {
			skipped++
		}
	}
	if skipped != 1 {
		t.Errorf("want exactly 1 skipped record, got %d: %+v", skipped, recs)
	}
	jobs, _ := s.List()
	if jobs[0].NextRun == nil || !jobs[0].NextRun.After(time.Now()) {
		t.Errorf("NextRun not advanced after skipped firing: %+v", jobs[0].NextRun)
	}
	close(exec.block)
	waitForRunCount(t, s, added.ID, 1)
}

func TestSchedulerWorkspaceScoping(t *testing.T) {
	s := openTestStore(t)
	exec := &fakeExecutor{}
	sch := New(s, exec, WithWorkspaceDir("/work/a"))

	past := time.Now().Add(-time.Second)
	if _, err := s.Add(Job{Name: "foreign", Kind: KindCron, Schedule: "* * * * *", Prompt: "p", Workspace: "/work/b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Job{Name: "legacy", Kind: KindCron, Schedule: "* * * * *", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Update(func(jobs *[]Job) error {
		for i := range *jobs {
			(*jobs)[i].NextRun = &past
		}
		return nil
	})
	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for exec.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if exec.count() != 1 {
		t.Fatalf("executor called %d times, want 1 (foreign job must not fire)", exec.count())
	}
	if got := exec.lastJobName(); got != "legacy" {
		t.Fatalf("fired job = %q, want legacy", got)
	}
	// The foreign job's NextRun must be untouched: another daemon owns it.
	jobs, _ := s.List()
	for _, j := range jobs {
		if j.Name == "foreign" && (j.NextRun == nil || !j.NextRun.Equal(past)) {
			t.Errorf("foreign job NextRun was touched: %+v", j.NextRun)
		}
	}
}

func TestRunRetriesFailedReconcile(t *testing.T) {
	s := openTestStore(t)
	// Corrupt jobs.json so reconcileOnStart fails.
	if err := os.WriteFile(s.Dir()+"/jobs.json", []byte("{bogus"), 0o600); err != nil {
		t.Fatal(err)
	}
	var reported int
	sch := New(s, &fakeExecutor{}, WithErrorReporter(func(err error) { reported++ }))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// Must return when ctx is cancelled, not with the reconcile error.
	if err := sch.Run(ctx); err != nil {
		t.Fatalf("Run returned %v, want nil (ctx cancellation)", err)
	}
	if reported == 0 {
		t.Error("reconcile failure was never reported")
	}
}

func TestStoreFilePermissions(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Add(Job{Name: "a", Kind: KindCron, Schedule: "0 9 * * *", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendRun(RunRecord{JobID: "x", JobName: "a"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		s.Dir():                 0o700,
		s.Dir() + "/jobs.json":  0o600,
		s.Dir() + "/runs.jsonl": 0o600,
		s.Dir() + "/jobs.lock":  0o600,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
	// Pre-existing loose files get tightened on Open.
	loose := t.TempDir()
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose+"/jobs.json", []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(loose); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		loose:                0o700,
		loose + "/jobs.json": 0o600,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}

func TestSchedulerOnceDisablesAfterRun(t *testing.T) {
	s := openTestStore(t)
	exec := &fakeExecutor{}
	sch := New(s, exec)

	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	_, err := s.Add(Job{Name: "once", Kind: KindOnce, Schedule: future, Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Second)
	_ = s.Update(func(jobs *[]Job) error {
		(*jobs)[0].NextRun = &past
		return nil
	})
	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		jobs, _ := s.List()
		if len(jobs) == 1 && jobs[0].RunCount == 1 {
			if jobs[0].Enabled {
				t.Error("once job still enabled after run")
			}
			if jobs[0].NextRun != nil {
				t.Error("once job still has NextRun after run")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("once job never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReconcileOnStart(t *testing.T) {
	s := openTestStore(t)
	sch := New(s, &fakeExecutor{})

	// Past-due once job, never ran: should be set to fire immediately.
	pastOnce := time.Now().Add(-time.Hour).Format(time.RFC3339)
	if _, err := s.Add(Job{Name: "late", Kind: KindOnce, Schedule: pastOnce, Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	// Cron job: next run recomputed from now (no catch-up to the past).
	if _, err := s.Add(Job{Name: "cron", Kind: KindCron, Schedule: "0 9 * * *", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}

	if err := sch.reconcileOnStart(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.List()
	byName := map[string]Job{}
	for _, j := range jobs {
		byName[j.Name] = j
	}
	late := byName["late"]
	if late.NextRun == nil || late.NextRun.After(time.Now().Add(time.Minute)) {
		t.Errorf("past-due once job not set to fire now: %+v", late.NextRun)
	}
	cronJob := byName["cron"]
	if cronJob.NextRun == nil || !cronJob.NextRun.After(time.Now()) {
		t.Errorf("cron job next run not in the future: %+v", cronJob.NextRun)
	}
}

func TestExecuteJobCompletesRunningRecord(t *testing.T) {
	s := openTestStore(t)
	exec := &fakeExecutor{}
	sch := New(s, exec)

	job, err := s.Add(Job{Name: "due", Kind: KindCron, Schedule: "* * * * *", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Second)
	_ = s.Update(func(jobs *[]Job) error {
		(*jobs)[0].NextRun = &past
		return nil
	})

	if err := sch.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForRunCount(t, s, job.ID, 1)

	// One dispatch must leave exactly one record: the "running" marker is
	// completed in place, not left dangling alongside the outcome.
	recs, err := s.ReadRuns(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("run records = %d, want exactly 1 (got %+v)", len(recs), recs)
	}
	rec := recs[0]
	if rec.ID == "" {
		t.Error("completed record has no ID")
	}
	if rec.Status != "ok" {
		t.Errorf("record status = %q, want ok", rec.Status)
	}
	if rec.FinishedAt.IsZero() || rec.FinishedAt.Before(rec.StartedAt) {
		t.Errorf("bad finished time: %+v", rec)
	}
}

func TestReconcileMarksInterruptedRuns(t *testing.T) {
	s := openTestStore(t)
	sch := New(s, &fakeExecutor{})

	job, err := s.Add(Job{Name: "once", Kind: KindOnce, Schedule: time.Now().Add(-time.Hour).Format(time.RFC3339), Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: the dispatch record was written, the process died
	// before the outcome landed. RunCount stays 0.
	if err := s.AppendRun(RunRecord{
		ID: NewRunID(), JobID: job.ID, JobName: job.Name,
		StartedAt: time.Now().Add(-time.Hour), Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	if err := sch.reconcileOnStart(); err != nil {
		t.Fatal(err)
	}

	recs, err := s.ReadRuns(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != "interrupted" {
		t.Fatalf("records after reconcile = %+v, want one interrupted", recs)
	}
	if recs[0].FinishedAt.IsZero() {
		t.Error("interrupted record has no finish time")
	}
	// The once job never completed, so it must be re-armed to fire now.
	jobs, _ := s.List()
	if jobs[0].NextRun == nil || jobs[0].NextRun.After(time.Now().Add(time.Minute)) {
		t.Errorf("interrupted once job not re-armed: %+v", jobs[0].NextRun)
	}
}

func TestReconcileRecordsMissedCronFiring(t *testing.T) {
	s := openTestStore(t)
	sch := New(s, &fakeExecutor{})

	if _, err := s.Add(Job{Name: "cron", Kind: KindCron, Schedule: "0 9 * * *", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	missed := time.Now().Add(-2 * time.Hour)
	_ = s.Update(func(jobs *[]Job) error {
		(*jobs)[0].NextRun = &missed
		return nil
	})

	if err := sch.reconcileOnStart(); err != nil {
		t.Fatal(err)
	}

	recs, err := s.ReadRuns(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != "missed" {
		t.Fatalf("records after reconcile = %+v, want one missed", recs)
	}
	if !recs[0].StartedAt.Equal(missed) {
		t.Errorf("missed record started at %v, want the missed firing %v", recs[0].StartedAt, missed)
	}
	// Stale prompts are not replayed: the next run is in the future.
	jobs, _ := s.List()
	if jobs[0].NextRun == nil || !jobs[0].NextRun.After(time.Now()) {
		t.Errorf("cron job not rescheduled to the future: %+v", jobs[0].NextRun)
	}
}

func TestUpdateRunNotFound(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpdateRun("nope", func(*RunRecord) {}); err == nil {
		t.Fatal("UpdateRun on missing id = nil, want error")
	}
}
