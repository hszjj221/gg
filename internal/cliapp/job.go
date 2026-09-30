package cliapp

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/daemon"
	"github.com/hszjj221/gg/internal/provider/openai"
	"github.com/hszjj221/gg/internal/scheduler"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/userprofile"
)

const jobUsage = `usage:
  gg job add --cron "0 9 * * *" --name NAME [--timezone TZ] [--allow-all] [--timeout 10m] PROMPT
  gg job add --at "2026-09-28 15:04" --name NAME [flags] PROMPT
  gg job add --in 20m --name NAME [flags] PROMPT
  gg job list [--all]
  gg job show <id|name>
  gg job pause|resume|remove <id|name>
  gg job log [--job <id|name>] [--limit N]
  gg job run <id|name>   # run once now; does not change the schedule`

// runJobCommand implements `gg job ...`: manage background scheduled jobs.
// Jobs only fire while the ggd daemon is alive; the CLI itself never runs the
// scheduling loop.
func runJobCommand(ctx context.Context, cfg config.Config, options Options, jobArgs []string, stdout, stderr io.Writer) int {
	if len(jobArgs) == 0 {
		fmt.Fprintln(stdout, jobUsage)
		return 2
	}
	store, err := scheduler.Open(cfg.Scheduler.Dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch jobArgs[0] {
	case "add":
		return runJobAdd(cfg, store, jobArgs[1:], stdout, stderr)
	case "list":
		return runJobList(store, jobArgs[1:], stdout, stderr)
	case "show":
		return runJobShow(store, jobArgs[1:], stdout, stderr)
	case "pause", "resume":
		return runJobToggle(store, jobArgs[0] == "resume", jobArgs[1:], stdout, stderr)
	case "remove":
		return runJobRemove(store, jobArgs[1:], stdout, stderr)
	case "log":
		return runJobLog(store, jobArgs[1:], stdout, stderr)
	case "run":
		return runJobRun(ctx, cfg, options, store, jobArgs[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, jobUsage)
		return 2
	}
}

func runJobAdd(cfg config.Config, store *scheduler.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg job add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cronExpr, atSpec, inSpec, name, timezone, timeoutSpec string
	var allowAll bool
	fs.StringVar(&cronExpr, "cron", "", "cron schedule (5-field, e.g. \"0 9 * * *\")")
	fs.StringVar(&atSpec, "at", "", "one-shot time (RFC3339 or \"2006-01-02 15:04\")")
	fs.StringVar(&inSpec, "in", "", "one-shot delay (e.g. 20m, 1h30m)")
	fs.StringVar(&name, "name", "", "job name")
	fs.StringVar(&timezone, "timezone", "", "IANA timezone (default: profile timezone, then local)")
	fs.StringVar(&timeoutSpec, "timeout", "", "per-run timeout (default 10m)")
	fs.BoolVar(&allowAll, "allow-all", false, "allow the unattended run to use tools that need approval (default: denied)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		fmt.Fprintln(stderr, "usage: gg job add (--cron EXPR|--at TIME|--in DURATION) --name NAME PROMPT")
		return 2
	}
	if timezone == "" {
		timezone = defaultJobTimezone(cfg)
	}
	job := scheduler.Job{
		Name:        strings.TrimSpace(name),
		Prompt:      prompt,
		Timezone:    timezone,
		Workspace:   cfg.CWD,
		AutoApprove: allowAll,
	}
	set := 0
	if cronExpr != "" {
		set++
		job.Kind = scheduler.KindCron
		job.Schedule = cronExpr
	}
	if atSpec != "" {
		set++
		job.Kind = scheduler.KindOnce
		job.Schedule = atSpec
	}
	if inSpec != "" {
		set++
		d, err := time.ParseDuration(inSpec)
		if err != nil || d <= 0 {
			fmt.Fprintln(stderr, fmt.Errorf("bad --in value %q: want a positive duration like 20m", inSpec))
			return 2
		}
		job.Kind = scheduler.KindOnce
		job.Schedule = time.Now().Add(d).Format(time.RFC3339)
	}
	if set != 1 {
		fmt.Fprintln(stderr, "specify exactly one of --cron, --at, --in")
		return 2
	}
	if timeoutSpec != "" {
		d, err := time.ParseDuration(timeoutSpec)
		if err != nil || d <= 0 {
			fmt.Fprintln(stderr, fmt.Errorf("bad --timeout value %q", timeoutSpec))
			return 2
		}
		job.Timeout = d
	}
	added, err := store.Add(job)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "job %s (%s) added; next run %s\n", added.ID, added.Name, formatJobTime(added.NextRun))
	if !daemon.DaemonAlive(cfg.HomeDir) {
		fmt.Fprintln(stderr, "note: ggd is not running; jobs fire only while the daemon is alive")
	}
	return 0
}

// defaultJobTimezone prefers the user profile timezone, falling back to local.
func defaultJobTimezone(cfg config.Config) string {
	if profile, err := userprofile.Load(cfg.UserFile); err == nil && profile.Timezone != "" {
		return profile.Timezone
	}
	return ""
}

func runJobList(store *scheduler.Store, args []string, stdout, stderr io.Writer) int {
	showAll := false
	for _, a := range args {
		if a == "--all" {
			showAll = true
		} else {
			fmt.Fprintln(stderr, "usage: gg job list [--all]")
			return 2
		}
	}
	jobs, err := store.List()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tKIND\tSCHEDULE\tNEXT RUN\tLAST RUN\tSTATUS")
	for _, job := range jobs {
		if !showAll && !job.Enabled {
			continue
		}
		status := "enabled"
		if !job.Enabled {
			status = "paused"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			job.ID, job.Name, job.Kind, job.Schedule,
			formatJobTime(job.NextRun), formatJobTime(job.LastRun), status)
	}
	w.Flush()
	return 0
}

func runJobShow(store *scheduler.Store, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: gg job show <id|name>")
		return 2
	}
	job, err := findJob(store, args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "id:          %s\nname:        %s\nkind:        %s\nschedule:    %s\ntimezone:    %s\nworkspace:   %s\nenabled:     %v\nauto-approve: %v\ntimeout:     %s\ncreated:     %s\nlast run:    %s\nnext run:    %s\nruns:        %d\nprompt:      %s\n",
		job.ID, job.Name, job.Kind, job.Schedule, displayTimezone(job), displayWorkspace(job),
		job.Enabled, job.AutoApprove, job.Timeout,
		job.CreatedAt.Format("2006-01-02 15:04"),
		formatJobTime(job.LastRun), formatJobTime(job.NextRun), job.RunCount, job.Prompt)
	return 0
}

func runJobToggle(store *scheduler.Store, enable bool, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: gg job pause|resume <id|name>")
		return 2
	}
	job, err := findJob(store, args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if enable && job.Kind == scheduler.KindOnce && job.RunCount > 0 {
		fmt.Fprintln(stderr, "job already ran once; use `gg job run` to fire it manually")
		return 1
	}
	err = store.Update(func(jobs *[]scheduler.Job) error {
		for i := range *jobs {
			if (*jobs)[i].ID == job.ID {
				(*jobs)[i].Enabled = enable
				(*jobs)[i].RefreshNext(time.Now())
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	verb := "paused"
	if enable {
		verb = "resumed"
	}
	note := ""
	if enable && job.Kind == scheduler.KindOnce && job.RunCount == 0 {
		// A never-run once job paused past its scheduled time has no next
		// occurrence; fire it immediately, like startup reconciliation does.
		if updated, err := findJob(store, job.ID); err == nil && updated.NextRun == nil {
			fire := time.Now()
			if err := store.Update(func(jobs *[]scheduler.Job) error {
				for i := range *jobs {
					if (*jobs)[i].ID == job.ID {
						(*jobs)[i].NextRun = &fire
					}
				}
				return nil
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			note = "; past due, will fire immediately"
		}
	}
	fmt.Fprintf(stdout, "job %s (%s) %s%s\n", job.ID, job.Name, verb, note)
	return 0
}

func runJobRemove(store *scheduler.Store, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: gg job remove <id|name>")
		return 2
	}
	job, err := findJob(store, args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	err = store.Update(func(jobs *[]scheduler.Job) error {
		kept := (*jobs)[:0]
		for _, j := range *jobs {
			if j.ID != job.ID {
				kept = append(kept, j)
			}
		}
		*jobs = kept
		return nil
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "job %s (%s) removed\n", job.ID, job.Name)
	return 0
}

func runJobLog(store *scheduler.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg job log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jobRef string
	var limit int
	fs.StringVar(&jobRef, "job", "", "only show runs for this job id|name")
	fs.IntVar(&limit, "limit", 20, "max records to show")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: gg job log [--job <id|name>] [--limit N]")
		return 2
	}
	recs, err := store.ReadRuns(jobLogReadLimit(jobRef, limit))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if jobRef != "" {
		recs = filterRunsByJobRef(recs, store, jobRef)
		if limit > 0 && len(recs) > limit {
			recs = recs[:limit]
		}
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "JOB\tSTARTED\tSTATUS\tSUMMARY")
	for _, r := range recs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.JobName, r.StartedAt.Format("2006-01-02 15:04"), r.Status, r.Summary)
	}
	w.Flush()
	return 0
}

// runJobRun fires a job immediately from the CLI by running one agent turn in
// this process. It does not go through the daemon's scheduler and does not
// change the job's schedule; the firing is recorded in the run log.
func runJobRun(ctx context.Context, cfg config.Config, options Options, store *scheduler.Store, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: gg job run <id|name>")
		return 2
	}
	job, err := findJob(store, args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	personal, notice, err := app.SetupPersonal(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if notice != "" {
		fmt.Fprintln(stderr, "note: "+notice)
	}
	skillSet := skills.Set{}
	if !options.NoSkills {
		var err error
		skillSet, err = skills.Load(skills.LoadOptions{CWD: cfg.CWD, HomeDir: options.HomeDir})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	repo := session.NewFileRepository(cfg.SessionDir)
	sessionStore, _, err := repo.Create(cfg.CWD)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := sessionStore.AppendName(fmt.Sprintf("scheduler/%s-manual-%s", job.Name, time.Now().Format("20060102-150405"))); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	providerFactory := options.ProviderFactory
	if providerFactory == nil {
		providerFactory = func(cfg config.Config) agent.Provider {
			return openai.NewClient(openai.Config{
				APIKey:  cfg.APIKey,
				BaseURL: cfg.BaseURL,
				Model:   cfg.Model,
				Compat: openai.Compat{
					NoStreamUsage:    cfg.Compat.NoStreamUsage,
					CompletionTokens: cfg.Compat.CompletionTokens,
				},
			})
		}
	}
	executor := app.NewService(app.Options{
		Config:          cfg,
		ProviderFactory: providerFactory,
		Store:           sessionStore,
		Skills:          skillSet,
		Profile:         personal.Profile,
		MemoryStore:     personal.Store,
	})
	prompt := fmt.Sprintf("[Manual trigger of scheduled job %q. You are running unattended: no human will see approval prompts, so only tools permitted by policy will execute. Complete the task and finish with a concise summary.]\n\n%s",
		job.Name, job.Prompt)
	approver := scheduler.UnattendedApprover{AllowAll: job.AutoApprove}
	timeout := job.Timeout
	if timeout <= 0 {
		timeout = scheduler.DefaultTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	code := runPrompt(rctx, executor, prompt, stdout, stderr, false, false, approver)
	rec := scheduler.RunRecord{
		JobID: job.ID, JobName: job.Name,
		StartedAt: started, FinishedAt: time.Now(),
		SessionPath: sessionStore.Path(),
		Status:      "ok",
		Summary:     "manual run from CLI",
	}
	if code != 0 {
		rec.Status = "error"
		if rctx.Err() == context.DeadlineExceeded {
			rec.Status = "timeout"
			rec.Summary = "manual run exceeded job timeout"
		}
	}
	if err := store.AppendRun(rec); err != nil {
		fmt.Fprintln(stderr, "warning: could not record the run:", err)
	}
	return code
}

// jobLogReadLimit decides how many run records to read: when filtering by
// job we must read the full log first and apply the limit after filtering,
// otherwise other jobs' records can crowd out the target's.
func jobLogReadLimit(jobRef string, limit int) int {
	if jobRef != "" {
		return 0 // all
	}
	return limit
}

// filterRunsByJobRef keeps records belonging to jobRef. A live definition is
// matched by id (name must already be unambiguous via findJob); a removed
// job is matched directly against the record's JobID or JobName so its
// append-only audit log stays accessible after `gg job remove`.
func filterRunsByJobRef(recs []scheduler.RunRecord, store *scheduler.Store, jobRef string) []scheduler.RunRecord {
	var filtered []scheduler.RunRecord
	if job, err := findJob(store, jobRef); err == nil {
		for _, r := range recs {
			if r.JobID == job.ID {
				filtered = append(filtered, r)
			}
		}
		return filtered
	}
	for _, r := range recs {
		if r.JobID == jobRef || r.JobName == jobRef {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// findJob locates a job by id, or by name when the name is unambiguous.
func findJob(store *scheduler.Store, ref string) (scheduler.Job, error) {
	jobs, err := store.List()
	if err != nil {
		return scheduler.Job{}, err
	}
	var byName []scheduler.Job
	for _, job := range jobs {
		if job.ID == ref {
			return job, nil
		}
		if job.Name == ref {
			byName = append(byName, job)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	if len(byName) > 1 {
		return scheduler.Job{}, fmt.Errorf("multiple jobs named %q; use the id", ref)
	}
	return scheduler.Job{}, fmt.Errorf("no job %q", ref)
}

func formatJobTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}

func displayTimezone(job scheduler.Job) string {
	if job.Timezone != "" {
		return job.Timezone
	}
	return "local"
}

func displayWorkspace(job scheduler.Job) string {
	if job.Workspace != "" {
		return job.Workspace
	}
	return "(any)"
}
