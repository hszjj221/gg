package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type reviewExecutor struct{ called chan struct{} }

func (e reviewExecutor) Execute(context.Context, Job) (string, string, error) {
	e.called <- struct{}{}
	return "done", "", nil
}
func TestFailedSaveReleasesRunningClaim(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Add(Job{Name: "review", Kind: KindCron, Schedule: "* * * * *", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	err = store.Update(func(js *[]Job) error { past := now.Add(-time.Minute); (*js)[0].NextRun = &past; return nil })
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(store.Dir(), "jobs.json.tmp")
	if err = os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	exec := reviewExecutor{called: make(chan struct{}, 1)}
	sched := New(store, exec)
	sched.clock = func() time.Time { return now }
	if err = sched.Tick(context.Background()); err == nil {
		t.Fatal("fault injection did not fail save")
	}
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err = sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exec.called:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not execute after storage recovered")
	}
	sched.workers.Wait()
	sched.mu.Lock()
	defer sched.mu.Unlock()
	if len(sched.running) != 0 {
		t.Fatal("executor claim was not released")
	}
}

func TestFailedTickPreservesExistingBusyClaim(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Add(Job{Name: "busy", Kind: KindCron, Schedule: "* * * * *", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = store.Update(func(js *[]Job) error { past := now.Add(-time.Minute); (*js)[0].NextRun = &past; return nil }); err != nil {
		t.Fatal(err)
	}
	sched := New(store, reviewExecutor{called: make(chan struct{}, 1)})
	sched.clock = func() time.Time { return now }
	sched.running[job.ID] = struct{}{}
	if err = os.Mkdir(filepath.Join(store.Dir(), "jobs.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = sched.Tick(context.Background()); err == nil {
		t.Fatal("save did not fail")
	}
	sched.mu.Lock()
	_, busy := sched.running[job.ID]
	sched.mu.Unlock()
	if !busy {
		t.Fatal("existing executor lost its claim")
	}
}
