package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/scheduler"
	"github.com/hszjj221/gg/internal/workspace"
)

// daemonJobExecutor runs scheduled jobs as agent turns inside the daemon's
// runtime. Each firing gets a fresh session named scheduler/<job>-<time> so
// runs are auditable and can be reopened with gg resume.
type daemonJobExecutor struct {
	rt     *app.Runtime
	logger *slog.Logger
}

func (e *daemonJobExecutor) Execute(ctx context.Context, job scheduler.Job) (string, string, error) {
	name := fmt.Sprintf("scheduler/%s-%s", sanitizeJobName(job.Name), time.Now().Format("20060102-150405"))
	e.logger.Info("scheduled job started", "job", job.Name, "session", name)
	snap, err := e.rt.CreateSessionInWorkspace(name, resolveJobWorkspaceRef(job, e.rt.WorkspaceRegistry()))
	if err != nil {
		return "", "", fmt.Errorf("create scheduler session: %w", err)
	}
	firedAt := time.Now().In(job.Location()).Format("2006-01-02 15:04 MST")
	prompt := fmt.Sprintf(
		"[Scheduled job %q fired at %s. You are running unattended: no human will see approval prompts, so only tools permitted by policy will execute. Complete the task and finish with a concise summary of what you did.]\n\n%s",
		job.Name, firedAt, job.Prompt,
	)
	approver := scheduler.UnattendedApprover{AllowAll: job.AutoApprove}
	run, err := e.rt.StartTurnWithApprover(ctx, snap.SessionID, prompt, approver)
	if err != nil {
		return "", snap.SessionPath, fmt.Errorf("start scheduled turn: %w", err)
	}
	summary, err := waitRunSummary(ctx, e.rt, run.ID())
	if err != nil {
		return "", snap.SessionPath, err
	}
	e.logger.Info("scheduled job finished", "job", job.Name, "session", name)
	return summary, snap.SessionPath, nil
}

// resolveJobWorkspaceRef picks the workspace a scheduled job runs in: the
// job's bound workspace ID first, then a legacy match of its recorded
// working directory against the registry, then "" — which
// CreateSessionInWorkspace interprets as the default workspace, where all
// jobs created before workspace binding landed.
func resolveJobWorkspaceRef(job scheduler.Job, reg *workspace.Registry) string {
	if job.WorkspaceID != "" {
		return job.WorkspaceID
	}
	if job.Workspace != "" {
		if ws, ok := reg.FindByRoot(job.Workspace); ok {
			return ws.ID
		}
	}
	return ""
}

// waitRunSummary drains run events until the run completes, fails, or is
// canceled, and returns the final text output.
func waitRunSummary(ctx context.Context, w *app.Runtime, runID string) (string, error) {
	var after int64
	for {
		events, done, err := w.WaitRun(ctx, runID, after)
		if err != nil {
			return "", fmt.Errorf("wait scheduled run: %w", err)
		}
		for _, ev := range events {
			after = ev.Sequence
			switch ev.Type {
			case app.EventRunCompleted:
				if ev.Result != nil {
					return strings.TrimSpace(ev.Result.Content), nil
				}
				return "", nil
			case app.EventRunFailed:
				if ev.Error != "" {
					return "", fmt.Errorf("scheduled run failed: %s", ev.Error)
				}
				return "", fmt.Errorf("scheduled run failed")
			case app.EventRunCanceled:
				return "", fmt.Errorf("scheduled run canceled")
			}
		}
		if done {
			return "", fmt.Errorf("scheduled run ended without a result")
		}
	}
}

// startScheduler loads persisted jobs and runs the scheduling loop in the
// background. Jobs fire as agent turns in the daemon's rt.
func newSchedulerChannel(cfg config.Config, rt *app.Runtime, logger *slog.Logger) (Channel, error) {
	store, err := scheduler.Open(cfg.Scheduler.Dir)
	if err != nil {
		return nil, fmt.Errorf("open scheduler store: %w", err)
	}
	sched := scheduler.New(store, &daemonJobExecutor{rt: rt, logger: logger},
		scheduler.WithErrorReporter(func(err error) {
			logger.Error("scheduled job failed", "error", err)
		}))
	return &schedulerChannel{sched: sched}, nil
}

type schedulerChannel struct {
	sched *scheduler.Scheduler
}

func (c *schedulerChannel) Name() string { return "scheduler" }

func (c *schedulerChannel) Run(ctx context.Context) error { return c.sched.Run(ctx) }
func sanitizeJobName(name string) string {
	var b strings.Builder
	lastDash := true // treat the start as a dash so leading junk is skipped
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
			lastDash = r == '-'
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "job"
	}
	if runes := []rune(s); len(runes) > 32 {
		s = string(runes[:32])
	}
	return s
}
