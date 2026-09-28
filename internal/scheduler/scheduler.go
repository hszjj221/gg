package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

// Executor runs one job firing. It is implemented by the daemon, which has
// access to the agent runtime; the core here stays testable with fakes.
// UnattendedApprover below is the one piece that depends on the agent
// package: it expresses the approval policy for unattended runs.
type Executor interface {
	// Execute runs job and returns a short human-readable summary plus the
	// session path where the run can be inspected.
	Execute(ctx context.Context, job Job) (summary string, sessionPath string, err error)
}

// UnattendedApprover is the approval policy for scheduled runs, where no
// human is present to answer prompts. By default every tool call that requires
// approval is denied (the agent can still use read-only tools and produce a
// text summary); a job created with --allow-all opts into full approval.
type UnattendedApprover struct {
	AllowAll bool
}

// Approve implements agent.Approver.
func (a UnattendedApprover) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return agent.ApprovalDecision{Allow: a.AllowAll}, nil
}

// idlePollInterval is how long the loop sleeps when no job is scheduled.
const idlePollInterval = 30 * time.Second

// summaryLimit caps the stored per-run summary.
const summaryLimit = 500

// Scheduler fires due jobs until ctx is cancelled.
type Scheduler struct {
	store *Store
	exec  Executor
	clock func() time.Time

	mu      sync.Mutex
	running map[string]struct{}

	// workspaceDir, when non-empty, scopes which jobs this scheduler fires:
	// jobs whose Workspace is set to a different directory are left for the
	// daemon that owns that workspace. Empty means fire everything (used by
	// tests and by jobs created before workspace tracking existed).
	workspaceDir string

	// reportError receives non-fatal operational errors (store failures,
	// run-record failures) so the host process can observe them. It must be
	// safe for concurrent use; nil means discard.
	reportError func(error)
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithErrorReporter sets the receiver for non-fatal operational errors.
func WithErrorReporter(fn func(error)) Option {
	return func(s *Scheduler) { s.reportError = fn }
}

// WithWorkspaceDir scopes the scheduler to jobs created from dir. The daemon
// passes its own workspace directory so a job created in workspace A is
// never executed by a daemon serving workspace B.
func WithWorkspaceDir(dir string) Option {
	return func(s *Scheduler) { s.workspaceDir = dir }
}

// New returns a Scheduler that persists state in store and runs jobs via exec.
func New(store *Store, exec Executor, opts ...Option) *Scheduler {
	s := &Scheduler{
		store:   store,
		exec:    exec,
		clock:   time.Now,
		running: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Scheduler) report(err error) {
	if err != nil && s.reportError != nil {
		s.reportError(err)
	}
}

// Run starts the scheduling loop and blocks until ctx is cancelled.
// On startup, cron jobs are rescheduled from now (missed firings are not
// caught up); once jobs that are past due and never ran fire immediately.
// The wait for the next firing is capped at idlePollInterval so jobs added or
// resumed by another process while the loop sleeps are picked up promptly.
// Transient store errors (including a failed startup reconciliation) are
// reported and retried instead of killing the loop.
func (s *Scheduler) Run(ctx context.Context) error {
	reconciled := false
	for {
		if !reconciled {
			if err := s.reconcileOnStart(); err != nil {
				s.report(fmt.Errorf("scheduler: reconcile on start: %w", err))
			} else {
				reconciled = true
			}
		}
		next, err := s.nextFireTime()
		wait := idlePollInterval
		if err != nil {
			s.report(fmt.Errorf("scheduler: read jobs: %w", err))
		} else if !next.IsZero() {
			if d := time.Until(next); d <= 0 {
				wait = 0
			} else if d < wait {
				wait = d
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			if err := s.Tick(ctx); err != nil {
				s.report(fmt.Errorf("scheduler: tick: %w", err))
			}
		}
	}
}

// owns reports whether this scheduler is responsible for firing job.
func (s *Scheduler) owns(job Job) bool {
	if s.workspaceDir == "" || job.Workspace == "" {
		return true
	}
	return filepath.Clean(job.Workspace) == filepath.Clean(s.workspaceDir)
}

// Tick fires every job whose NextRun has passed and returns the first store
// error encountered, if any. It is a single loop iteration, exposed so tests
// can drive the scheduler deterministically.
//
// Each due job's NextRun is advanced in the store before its goroutine is
// dispatched, so the loop never sees the same firing as due again while the
// run is in flight (previously this spun at full speed writing "skipped"
// records for the whole execution). A job that is due while its previous run
// is still in flight is recorded once as skipped and its NextRun advanced as
// well, so the loop still cannot spin.
func (s *Scheduler) Tick(ctx context.Context) error {
	now := s.clock()
	var due []Job
	busy := map[string]bool{}
	err := s.store.Update(func(jobs *[]Job) error {
		for i := range *jobs {
			job := &(*jobs)[i]
			if !job.Enabled || job.NextRun == nil || job.NextRun.After(now) {
				continue
			}
			if !s.owns(*job) {
				continue
			}
			s.mu.Lock()
			_, isBusy := s.running[job.ID]
			if !isBusy {
				s.running[job.ID] = struct{}{}
			}
			s.mu.Unlock()
			busy[job.ID] = isBusy
			due = append(due, *job)
			// Advance before dispatch: the persisted schedule moves on even
			// if this process crashes mid-run.
			job.RefreshNext(now)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, job := range due {
		if busy[job.ID] {
			s.recordRun(RunRecord{
				JobID: job.ID, JobName: job.Name,
				StartedAt: now, FinishedAt: now,
				Status: "skipped", Summary: "previous run still in progress",
			})
			continue
		}
		go s.executeJob(ctx, job)
	}
	return nil
}

// reconcileOnStart reschedules everything relative to now.
//
// Crash recovery happens here:
//   - run records still marked "running" (dispatched but never completed)
//     are marked "interrupted": the daemon stopped mid-run. A once job in
//     this state has RunCount == 0, so it is re-armed below and retried;
//     cron jobs simply continue on their refreshed schedule.
//   - cron jobs whose NextRun already passed while the daemon was down get
//     one "missed" record (stale prompts are not replayed, but the gap is
//     visible in the run history).
func (s *Scheduler) reconcileOnStart() error {
	now := s.clock()
	// Recovery failures are returned (not merely reported): the Run loop
	// retries reconciliation until it succeeds, so dangling "running"
	// records cannot be left behind permanently.
	runs, err := s.store.ReadRuns(0)
	if err != nil {
		return fmt.Errorf("scheduler: read run records for recovery: %w", err)
	}
	for _, rec := range runs {
		if rec.Status != "running" {
			continue
		}
		rec := rec
		if err := s.store.UpdateRun(rec.ID, func(r *RunRecord) {
			r.Status = "interrupted"
			r.FinishedAt = now
			r.Summary = "daemon stopped while the run was in flight"
		}); err != nil {
			return fmt.Errorf("scheduler: mark run %s interrupted: %w", rec.ID, err)
		}
	}
	// Missed firings are collected here and recorded after the update: the
	// store lock is already held inside Update, and recordRun takes it
	// again (flock is per-fd, so re-locking in this process would
	// deadlock).
	var missed []RunRecord
	if err := s.store.Update(func(jobs *[]Job) error {
		for i := range *jobs {
			job := &(*jobs)[i]
			if !job.Enabled {
				job.NextRun = nil
				continue
			}
			if job.Kind == KindOnce {
				// A once job that never ran and is past due fires immediately;
				// one that already ran stays disabled (executeJob disables it).
				if job.RunCount == 0 {
					if _, ok := job.NextAfter(now); !ok {
						fire := now
						job.NextRun = &fire
					} else {
						job.RefreshNext(now)
					}
				} else {
					job.Enabled = false
					job.NextRun = nil
				}
				continue
			}
			if job.NextRun != nil && job.NextRun.Before(now) {
				missedAt := *job.NextRun
				missed = append(missed, RunRecord{
					ID: NewRunID(), JobID: job.ID, JobName: job.Name,
					StartedAt: missedAt, FinishedAt: now, Status: "missed",
					Summary: fmt.Sprintf("scheduled firing at %s missed: daemon was not running", missedAt.Format(time.RFC3339)),
				})
			}
			job.RefreshNext(now)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, rec := range missed {
		s.recordRun(rec)
	}
	return nil
}

func (s *Scheduler) nextFireTime() (time.Time, error) {
	var next time.Time
	err := s.store.View(func(jobs []Job) error {
		for _, job := range jobs {
			if !job.Enabled || job.NextRun == nil {
				continue
			}
			if !s.owns(job) {
				continue
			}
			if next.IsZero() || job.NextRun.Before(next) {
				next = *job.NextRun
			}
		}
		return nil
	})
	return next, err
}

func (s *Scheduler) executeJob(ctx context.Context, job Job) {
	defer func() {
		s.mu.Lock()
		delete(s.running, job.ID)
		s.mu.Unlock()
	}()
	timeout := job.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ectx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := s.clock()
	// Record the dispatch before executing: if this process crashes mid-run,
	// the dangling "running" record lets the next startup mark the run as
	// interrupted instead of losing it silently.
	runID := NewRunID()
	s.recordRun(RunRecord{
		ID: runID, JobID: job.ID, JobName: job.Name,
		StartedAt: started, Status: "running",
	})
	summary, sessionPath, err := s.exec.Execute(ectx, job)
	finished := s.clock()

	rec := RunRecord{
		JobID: job.ID, JobName: job.Name,
		StartedAt: started, FinishedAt: finished,
		SessionPath: sessionPath,
	}
	switch {
	case err == nil:
		rec.Status = "ok"
		rec.Summary = truncate(summary, summaryLimit)
	case ectx.Err() == context.DeadlineExceeded:
		rec.Status = "timeout"
		rec.Summary = truncate("job exceeded timeout: "+err.Error(), summaryLimit)
	default:
		rec.Status = "error"
		rec.Summary = truncate(err.Error(), summaryLimit)
	}
	if uerr := s.store.UpdateRun(runID, func(r *RunRecord) {
		r.FinishedAt = rec.FinishedAt
		r.Status = rec.Status
		r.Summary = rec.Summary
		r.SessionPath = rec.SessionPath
	}); uerr != nil {
		// The update raced with something unexpected (e.g. the log was
		// rotated out of band); fall back to appending so the outcome is
		// not lost.
		s.recordRun(rec)
	}

	if err := s.store.Update(func(jobs *[]Job) error {
		for i := range *jobs {
			if (*jobs)[i].ID != job.ID {
				continue
			}
			st := &(*jobs)[i]
			st.LastRun = &finished
			st.RunCount++
			if st.Kind == KindOnce {
				st.Enabled = false
				st.NextRun = nil
			} else {
				st.RefreshNext(finished)
			}
		}
		return nil
	}); err != nil {
		s.report(fmt.Errorf("scheduler: update job %s after run: %w", job.ID, err))
	}
}

func (s *Scheduler) recordRun(rec RunRecord) {
	if err := s.store.AppendRun(rec); err != nil {
		s.report(fmt.Errorf("scheduler: append run record for job %s: %w", rec.JobID, err))
	}
}

// truncate shortens s to at most n runes so multi-byte text is never split.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
