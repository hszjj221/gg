// Package scheduler provides cron-like background job scheduling for the gg
// daemon. Jobs are persisted under ~/.gg/scheduler and fire while ggd is
// alive; the CLI (gg job ...) manages them from any process.
package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Kind distinguishes one-shot jobs from recurring cron jobs.
type Kind string

const (
	KindOnce Kind = "once"
	KindCron Kind = "cron"
)

// DefaultTimeout bounds a single job execution.
const DefaultTimeout = 10 * time.Minute

// Job is a scheduled unit of work: at the scheduled time the daemon wakes an
// agent with Job.Prompt.
type Job struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      Kind   `json:"kind"`
	Schedule  string `json:"schedule"` // cron: 5-field expression; once: RFC3339 timestamp
	Prompt    string `json:"prompt"`
	Timezone  string `json:"timezone"`
	Workspace string `json:"workspace,omitempty"` // workspace dir the job was created from (legacy; kept for jobs created before workspace IDs were bound)
	// WorkspaceID is the stable workspace the job runs in. It takes
	// precedence over Workspace; empty means "the default workspace"
	// (jobs created before workspace binding existed).
	WorkspaceID string        `json:"workspace_id,omitempty"`
	Enabled     bool          `json:"enabled"`
	AutoApprove bool          `json:"auto_approve"`
	Timeout     time.Duration `json:"timeout"`
	CreatedAt   time.Time     `json:"created_at"`
	LastRun     *time.Time    `json:"last_run,omitempty"`
	NextRun     *time.Time    `json:"next_run,omitempty"`
	RunCount    int           `json:"run_count"`
}

// RunRecord is an append-only log entry for one job execution.
// Statuses: running (dispatched, not finished), ok, error, timeout,
// skipped (a previous run was still in flight), missed (the daemon was down
// at the scheduled time), interrupted (the daemon stopped mid-run).
type RunRecord struct {
	ID          string    `json:"id,omitempty"`
	JobID       string    `json:"job_id"`
	JobName     string    `json:"job_name"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary,omitempty"`
	SessionPath string    `json:"session_path,omitempty"`
}

// cronParser accepts the standard 5-field format plus @-descriptors.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// NewID returns a short random job identifier.
func NewID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// NewRunID returns a random identifier for one job execution, used to pair a
// "running" record with its final outcome. Run IDs accumulate in an
// unbounded history, so they carry 128 random bits: NewID's 24 bits would
// reach ~50% collision probability after only a few thousand executions,
// and a repeated ID would make UpdateRun complete the wrong (older) record,
// leaving the new marker permanently "running".
func NewRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

// ParseCron validates a 5-field cron expression (or @-descriptor).
func ParseCron(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("empty cron expression")
	}
	return cronParser.Parse(expr)
}

// onceLayouts are accepted for --at timestamps.
var onceLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseOnce parses an --at timestamp. Naive inputs are interpreted in loc.
func ParseOnce(spec string, loc *time.Location) (time.Time, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	var errs []string
	for _, layout := range onceLayouts {
		if t, err := time.ParseInLocation(layout, spec, loc); err == nil {
			return t, nil
		} else {
			errs = append(errs, err.Error())
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q (want RFC3339 or 2006-01-02 15:04): %s", spec, strings.Join(errs, "; "))
}

// Location resolves the job's timezone, falling back to local time.
func (j Job) Location() *time.Location {
	if j.Timezone != "" {
		if loc, err := time.LoadLocation(j.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

// Validate checks a job definition before it is stored.
func (j Job) Validate() error {
	if strings.TrimSpace(j.Name) == "" {
		return fmt.Errorf("job name is required")
	}
	if strings.TrimSpace(j.Prompt) == "" {
		return fmt.Errorf("job prompt is required")
	}
	if j.Timezone != "" {
		if _, err := time.LoadLocation(j.Timezone); err != nil {
			return fmt.Errorf("bad timezone %q: %w", j.Timezone, err)
		}
	}
	switch j.Kind {
	case KindCron:
		if _, err := ParseCron(j.Schedule); err != nil {
			return fmt.Errorf("bad cron schedule: %w", err)
		}
	case KindOnce:
		if _, err := ParseOnce(j.Schedule, j.Location()); err != nil {
			return fmt.Errorf("bad once schedule: %w", err)
		}
	default:
		return fmt.Errorf("unknown job kind %q", j.Kind)
	}
	if j.Timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	return nil
}

// NextAfter returns the next fire time strictly after t. ok is false when a
// once job's time has passed.
func (j Job) NextAfter(t time.Time) (next time.Time, ok bool) {
	loc := j.Location()
	t = t.In(loc)
	switch j.Kind {
	case KindCron:
		sched, err := ParseCron(j.Schedule)
		if err != nil {
			return time.Time{}, false
		}
		return sched.Next(t), true
	case KindOnce:
		at, err := ParseOnce(j.Schedule, loc)
		if err != nil {
			return time.Time{}, false
		}
		if !at.After(t) {
			return time.Time{}, false
		}
		return at, true
	default:
		return time.Time{}, false
	}
}

// RefreshNext recomputes NextRun relative to t.
func (j *Job) RefreshNext(t time.Time) {
	if !j.Enabled {
		j.NextRun = nil
		return
	}
	if next, ok := j.NextAfter(t); ok {
		j.NextRun = &next
	} else {
		j.NextRun = nil
	}
}
