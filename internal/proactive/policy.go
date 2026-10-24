// Package proactive decides when to surface local digests or capture candidates.
// It does not execute tools or grant authorization.
package proactive

import (
	"fmt"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

type Decision string

const (
	Summarize Decision = "Summarize"
	Quiet     Decision = "Quiet"
	NoteOnly  Decision = "Note-only"
)

type Scenario string

const (
	EmailSummary      Scenario = "email_summary"
	CalendarReminder  Scenario = "calendar_reminder"
	StandingAuthCheck Scenario = "standing_auth_check"
)

// Policy's zero value permits local personal digests outside quiet hours.
// OS notifications are disabled (maximum zero per run), with no opt-in in P4.
type Policy struct {
	// QuietHours is empty (disabled) or HH:MM-HH:MM in Input.Now's location.
	// Start is inclusive, end exclusive; overnight windows are supported.
	QuietHours string
}

type Input struct {
	Profile       string
	Scenario      Scenario
	Now           time.Time
	RememberLater bool
	Notify        bool
	// ProposedAction, when non-nil, must still go through the existing tool
	// approver. Nil means only local stdout/candidate output is proposed.
	ProposedAction *agent.ApprovalRequest
}

// Result describes local delivery only. Neither Summarize nor NoteOnly allows
// a tool call: in particular, promoting a candidate via memory_add needs approval.
type Result struct {
	Decision Decision
	Reason   string
}

func (p Policy) Validate() error {
	_, _, err := p.window()
	return err
}

// Decide is deterministic and has no I/O, model, clock, or grant-store access.
func (p Policy) Decide(in Input) Result {
	if in.Profile != config.ProfilePersonal {
		return Result{Quiet, "proactivity disabled outside personal profile"}
	}
	switch in.Scenario {
	case EmailSummary, CalendarReminder, StandingAuthCheck:
	default:
		return Result{Quiet, "unsupported scenario"}
	}
	if in.Notify {
		return Result{Quiet, "OS notify disabled; maximum notifications per run is zero"}
	}
	if in.ProposedAction != nil {
		if in.ProposedAction.RequiresHumanConfirm() {
			return Result{Quiet, "E/M/U requires human approval or a matching standing grant"}
		}
		return Result{Quiet, "tool actions require the existing approver; policy is not authorization"}
	}
	start, end, err := p.window()
	if err != nil {
		return Result{Quiet, "invalid quiet hours"}
	}
	// An explicit request to capture for later stays private, even during
	// quiet hours. It is a candidate, not a durable memory insertion.
	if in.RememberLater {
		return Result{NoteOnly, "capture a local candidate for later; do not surface its content now"}
	}
	if p.QuietHours != "" {
		if in.Now.IsZero() {
			return Result{Quiet, "quiet hours require a local time"}
		}
		minute := in.Now.Hour()*60 + in.Now.Minute()
		if (start < end && minute >= start && minute < end) ||
			(start > end && (minute >= start || minute < end)) {
			return Result{Quiet, "quiet hours"}
		}
	}
	if in.Scenario == StandingAuthCheck {
		return Result{Quiet, "authorization checks do not produce digests"}
	}
	return Result{Summarize, "brief local digest only"}
}

func (p Policy) window() (int, int, error) {
	if p.QuietHours == "" {
		return 0, 0, nil
	}
	startText, endText, ok := strings.Cut(p.QuietHours, "-")
	start, startErr := time.Parse("15:04", startText)
	end, endErr := time.Parse("15:04", endText)
	if !ok || startErr != nil || endErr != nil ||
		start.Format("15:04") != startText || end.Format("15:04") != endText || start.Equal(end) {
		return 0, 0, fmt.Errorf("quiet hours must be HH:MM-HH:MM with different start and end")
	}
	return start.Hour()*60 + start.Minute(), end.Hour()*60 + end.Minute(), nil
}
