package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	jobsFileName = "jobs.json"
	runsFileName = "runs.jsonl"
	lockFileName = "jobs.lock"
)

// Store persists job definitions and run records under dir
// (~/.gg/scheduler). All read-modify-write cycles on jobs.json are serialized
// with an flock on jobs.lock so the CLI and the daemon can operate
// concurrently without corrupting the file.
type Store struct {
	dir string
}

// Open creates dir if needed and returns a Store rooted there.
// Job prompts and run summaries can contain private material, so the
// directory and state files are restricted to the owner (0600/0700), matching
// session and memory persistence elsewhere in gg. Pre-existing installs get
// their permissions tightened on open (best effort).
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("scheduler dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create scheduler dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	_ = os.Chmod(filepath.Join(dir, jobsFileName), 0o600)
	_ = os.Chmod(filepath.Join(dir, runsFileName), 0o600)
	return &Store{dir: dir}, nil
}

// Dir returns the store's root directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) jobsPath() string { return filepath.Join(s.dir, jobsFileName) }
func (s *Store) runsPath() string { return filepath.Join(s.dir, runsFileName) }
func (s *Store) lockPath() string { return filepath.Join(s.dir, lockFileName) }

// Update loads the job list, applies fn, and saves it back atomically, all
// under an exclusive lock.
func (s *Store) Update(fn func(jobs *[]Job) error) error {
	if fn == nil {
		return fmt.Errorf("scheduler: Update requires a mutation function (use View for reads)")
	}
	return s.withLock(true, fn)
}

// View loads the job list and passes a consistent snapshot to fn under an
// exclusive lock. Unlike Update it never rewrites jobs.json, so pure reads
// don't churn the file or its mtime.
func (s *Store) View(fn func(jobs []Job) error) error {
	if fn == nil {
		return fmt.Errorf("scheduler: View requires a function")
	}
	return s.withLock(false, func(jobs *[]Job) error { return fn(*jobs) })
}

func (s *Store) withLock(write bool, fn func(jobs *[]Job) error) error {
	return s.withFileLock(func() error {
		jobs, err := s.loadJobs()
		if err != nil {
			return err
		}
		if err := fn(&jobs); err != nil {
			return err
		}
		if write {
			if err := s.saveJobs(jobs); err != nil {
				return err
			}
		}
		return nil
	})
}

// withFileLock runs fn while holding the store's cross-process lock. The
// same lock guards jobs.json and the run log: run-log rewrites
// (UpdateRun) are read-modify-rename, so they must be serialized against
// concurrent appends (AppendRun) from other daemon goroutines or from
// `gg job run`, otherwise the rename can silently discard records.
func (s *Store) withFileLock(fn func() error) error {
	lock, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open scheduler lock: %w", err)
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("lock scheduler store: %w", err)
	}
	defer unlockFile(lock)
	return fn()
}

// List returns a consistent snapshot of all jobs.
func (s *Store) List() ([]Job, error) {
	var jobs []Job
	if err := s.View(func(js []Job) error {
		jobs = append([]Job(nil), js...)
		return nil
	}); err != nil {
		return nil, err
	}
	return jobs, nil
}

// Add validates the job, assigns defaults, and stores it.
func (s *Store) Add(job Job) (Job, error) {
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	if job.ID == "" {
		job.ID = NewID()
	}
	job.Enabled = true
	if job.Timeout <= 0 {
		job.Timeout = DefaultTimeout
	}
	job.CreatedAt = time.Now()
	job.RefreshNext(job.CreatedAt)
	if err := s.Update(func(jobs *[]Job) error {
		*jobs = append(*jobs, job)
		return nil
	}); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Store) loadJobs() ([]Job, error) {
	data, err := os.ReadFile(s.jobsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read jobs: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("parse jobs.json: %w", err)
	}
	return jobs, nil
}

func (s *Store) saveJobs(jobs []Job) error {
	if jobs == nil {
		jobs = []Job{}
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return fmt.Errorf("encode jobs: %w", err)
	}
	tmp := s.jobsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write jobs tmp: %w", err)
	}
	if err := os.Rename(tmp, s.jobsPath()); err != nil {
		return fmt.Errorf("commit jobs: %w", err)
	}
	return nil
}

// AppendRun appends one run record. The file is opened O_APPEND and the
// record is written with a single Write call, so concurrent O_APPEND writers
// on Linux/macOS cannot interleave or overwrite each other; a torn record
// from a crash is skipped by ReadRuns. A short write is treated as an error.
// The store lock is held so the append cannot slip between UpdateRun's read
// and rename and be silently discarded.
func (s *Store) AppendRun(rec RunRecord) error {
	return s.withFileLock(func() error {
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("encode run record: %w", err)
		}
		data = append(data, '\n')
		f, err := os.OpenFile(s.runsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open runs log: %w", err)
		}
		defer f.Close()
		n, err := f.Write(data)
		if err != nil {
			return fmt.Errorf("append run record: %w", err)
		}
		if n != len(data) {
			return fmt.Errorf("append run record: short write (%d of %d bytes)", n, len(data))
		}
		return nil
	})
}

// UpdateRun rewrites the run record with the given id in place, applying fn
// to it. Run records are normally append-only; this exists so a "running"
// record written at dispatch can be completed with its final outcome, and so
// startup reconciliation can mark records left dangling by a crash. The file
// is rewritten atomically via temp file + rename. Completions are rare
// (one per job execution), so the rewrite cost is negligible. The store lock
// is held for the whole read-modify-rename so a concurrent AppendRun cannot
// be discarded by the rename.
func (s *Store) UpdateRun(id string, fn func(*RunRecord)) error {
	return s.withFileLock(func() error {
		data, err := os.ReadFile(s.runsPath())
		if err != nil {
			return fmt.Errorf("read runs log: %w", err)
		}
		lines := splitLines(data)
		found := false
		for i, line := range lines {
			var rec RunRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				continue
			}
			if rec.ID != id {
				continue
			}
			fn(&rec)
			enc, err := json.Marshal(rec)
			if err != nil {
				return fmt.Errorf("encode run record: %w", err)
			}
			lines[i] = enc
			found = true
			break
		}
		if !found {
			return fmt.Errorf("run record %q not found", id)
		}
		var buf []byte
		for _, line := range lines {
			buf = append(buf, line...)
			buf = append(buf, '\n')
		}
		tmp, err := os.CreateTemp(s.dir, "runs-*.tmp")
		if err != nil {
			return fmt.Errorf("create temp runs log: %w", err)
		}
		tmpName := tmp.Name()
		if _, err := tmp.Write(buf); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("write temp runs log: %w", err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("close temp runs log: %w", err)
		}
		if err := os.Chmod(tmpName, 0o600); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("chmod temp runs log: %w", err)
		}
		if err := os.Rename(tmpName, s.runsPath()); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("commit runs log: %w", err)
		}
		return nil
	})
}

// ReadRuns returns the most recent run records, newest first, up to limit
// (limit <= 0 means all). The store lock is held so the read cannot observe
// the log mid-rewrite.
func (s *Store) ReadRuns(limit int) ([]RunRecord, error) {
	var recs []RunRecord
	if err := s.withFileLock(func() error {
		data, err := os.ReadFile(s.runsPath())
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("read runs log: %w", err)
		}
		for _, line := range splitLines(data) {
			var rec RunRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				continue // tolerate a torn last line
			}
			recs = append(recs, rec)
		}
		// Newest first.
		for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
			recs[i], recs[j] = recs[j], recs[i]
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}
	return recs, nil
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}
