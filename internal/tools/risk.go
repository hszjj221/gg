package tools

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/runlog"
)

// RiskAction is what the bash risk check decided for one command.
// Lists are applied before any model call. A list hit is final.
type RiskAction int

const (
	// RiskDefer keeps gg's existing confirmation policy for this command.
	RiskDefer RiskAction = iota
	// RiskRun executes without asking.
	RiskRun
	// RiskAsk requires a person to confirm. When nobody can answer, the
	// approver denies. This action never means "run".
	RiskAsk
)

func (a RiskAction) String() string {
	switch a {
	case RiskRun:
		return "run"
	case RiskAsk:
		return "ask"
	default:
		return "defer"
	}
}

// RiskVerdict is the outcome of RiskJudge.Judge.
type RiskVerdict struct {
	Action RiskAction
	// Dangerous is set only for a classifier denylist hit. Approvers keep
	// the existing dangerous-command policy for it (not waived by --yolo
	// or a job's --allow-all). A model "ask" or a failed check leaves
	// Dangerous false and is still not auto-approved.
	Dangerous bool
	// Reason is shown on the approval request. It never includes secrets.
	Reason string
	// Source is "allowlist", "denylist", "hold", "model", "fallback", or
	// "policy". It is for logs and tests.
	Source string
}

// RiskJudge classifies one bash command before the approver runs.
// Implementations must not auto-run a command the denylist matches, and
// must not treat an error as permission to run.
type RiskJudge interface {
	Judge(ctx context.Context, command, cwd string) RiskVerdict
}

// ModelScorer is the model half of the risk check. Score returns the
// probability that the command needs confirmation (Jev's noul for
// needs_confirm). Any error asks the user; it must not be mapped to a run.
type ModelScorer interface {
	Score(ctx context.Context, command, cwd, goos string) (float64, error)
}

// RiskOptions configures the two-layer bash risk check.
//
// The API key is not a field. The Jev client reads TYPESAFE_API_KEY from
// the environment at request time and fails closed when it is empty.
// Enabled is the startup snapshot of that variable; with Enabled false
// the model is never called and list-only classification is unchanged.
type RiskOptions struct {
	Enabled    bool
	Endpoint   string
	RunBelow   float64
	AskAbove   float64
	Timeout    time.Duration
	OS         string
	Model      ModelScorer
	HTTPClient *http.Client
}

// NewCommandRiskJudge builds the bash risk check. A disabled judge (no
// API key) applies only the classifier lists.
func NewCommandRiskJudge(opts RiskOptions) RiskJudge {
	j := &commandRiskJudge{
		runBelow: opts.RunBelow,
		askAbove: opts.AskAbove,
		goos:     opts.OS,
	}
	if j.goos == "" {
		j.goos = runtime.GOOS
	}
	if opts.Model != nil {
		j.model = opts.Model
		j.timeout = opts.Timeout
		return j
	}
	if !opts.Enabled {
		return j
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultJevTimeout
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = defaultJevEndpoint
	}
	j.timeout = timeout
	j.model = newJevScorer(endpoint, timeout, opts.HTTPClient)
	return j
}

// disabledCommandRisk applies classifier lists only. It is the behavior
// when TYPESAFE_API_KEY is unset.
var disabledCommandRisk RiskJudge = &commandRiskJudge{}

type commandRiskJudge struct {
	model    ModelScorer
	runBelow float64
	askAbove float64
	timeout  time.Duration
	goos     string
}

func (j *commandRiskJudge) Judge(ctx context.Context, command, cwd string) RiskVerdict {
	if j == nil {
		return listVerdict(ClassifyCommand(command, cwd))
	}
	class := ClassifyCommand(command, cwd)
	switch class.Risk {
	case agent.ApprovalRiskReadOnly:
		return RiskVerdict{Action: RiskRun, Reason: class.Reason, Source: "allowlist"}
	case agent.ApprovalRiskDangerous:
		return RiskVerdict{Action: RiskAsk, Dangerous: true, Reason: class.Reason, Source: "denylist"}
	}
	if j.model == nil {
		return RiskVerdict{Action: RiskDefer, Source: "policy"}
	}
	if class.Hold {
		return j.logged(ctx, RiskVerdict{
			Action: RiskAsk,
			Reason: "command could not be classified safely; asking for confirmation",
			Source: "hold",
		})
	}
	callCtx := ctx
	if j.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, j.timeout)
		defer cancel()
	}
	noul, err := j.model.Score(callCtx, command, cwd, j.goos)
	if err != nil {
		return j.logged(ctx, RiskVerdict{Action: RiskAsk, Reason: fallbackReason(err), Source: "fallback"})
	}
	if noul < 0 || noul > 1 {
		return j.logged(ctx, RiskVerdict{Action: RiskAsk, Reason: fallbackBadResponse, Source: "fallback"})
	}
	if noul < j.runBelow {
		return j.logged(ctx, RiskVerdict{
			Action: RiskRun,
			Reason: "jev noul " + formatProb(noul) + " < " + formatProb(j.runBelow) + "; run without confirmation",
			Source: "model",
		})
	}
	if noul > j.askAbove {
		return j.logged(ctx, RiskVerdict{
			Action: RiskAsk,
			Reason: "jev noul " + formatProb(noul) + " > " + formatProb(j.askAbove) + "; confirmation required",
			Source: "model",
		})
	}
	return j.logged(ctx, RiskVerdict{
		Action: RiskDefer,
		Reason: "jev noul " + formatProb(noul) + "; existing confirmation policy",
		Source: "model",
	})
}

func listVerdict(class CommandClassification) RiskVerdict {
	switch class.Risk {
	case agent.ApprovalRiskReadOnly:
		return RiskVerdict{Action: RiskRun, Reason: class.Reason, Source: "allowlist"}
	case agent.ApprovalRiskDangerous:
		return RiskVerdict{Action: RiskAsk, Dangerous: true, Reason: class.Reason, Source: "denylist"}
	default:
		return RiskVerdict{Action: RiskDefer, Source: "policy"}
	}
}

func (j *commandRiskJudge) logged(ctx context.Context, v RiskVerdict) RiskVerdict {
	runlog.Logger(ctx, slog.Default()).DebugContext(ctx, "bash risk decision",
		"source", v.Source,
		"action", v.Action.String(),
		"dangerous", v.Dangerous,
	)
	return v
}

func formatProb(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

const fallbackBadResponse = "jev risk check returned an unreadable response; asking for confirmation"

func fallbackReason(err error) string {
	if err == nil {
		return fallbackBadResponse
	}
	if errorsIsTimeout(err) {
		return "jev risk check timed out; asking for confirmation"
	}
	if err == errJevUnconfigured {
		return "jev risk check has no API key; asking for confirmation"
	}
	if err == errJevResponse {
		return fallbackBadResponse
	}
	return "jev risk check failed; asking for confirmation"
}
