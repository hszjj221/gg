package proactive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/standingauth"
	"github.com/hszjj221/gg/internal/tools"
)

// DigestSource supplies bodies for local digests. Implementations must not
// send mail, create events, draft, notify, execute tools, or call a model.
type DigestSource interface {
	EmailSummary(ctx context.Context, now time.Time) (string, error)
	CalendarReminder(ctx context.Context, now time.Time) (string, error)
}

type DigestOptions struct {
	Profile       string
	QuietHours    string
	Now           time.Time // zero uses the local wall clock
	RememberLater bool
	HomeDir       string
	Writer        io.Writer    // nil suppresses stdout, including digest bodies
	Source        DigestSource // required for personal digests; tests inject fakes
}

type DigestResult struct {
	Directory string
	Summary   string // decisions and paths only, suitable for scheduler history
}

// RunDigest applies Policy before fetching inbox/agenda bodies for local output.
// It never constructs a model, executes a tool, or loads real standing grants.
func RunDigest(ctx context.Context, opts DigestOptions) (DigestResult, error) {
	var result DigestResult
	policy := Policy{QuietHours: opts.QuietHours}
	if err := policy.Validate(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	w := opts.Writer
	if w == nil {
		w = io.Discard
	}
	if opts.Profile != config.ProfilePersonal {
		result.Summary = "Quiet: proactivity disabled outside personal profile."
		_, err := fmt.Fprintln(w, result.Summary)
		return result, err
	}
	if opts.Source == nil {
		return result, fmt.Errorf("digest source is required (live Google reads require: gg connect google)")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	if opts.HomeDir == "" {
		return result, fmt.Errorf("digest home directory is required")
	}
	root := filepath.Join(opts.HomeDir, ".gg", "proactive")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return result, err
	}
	// A unique directory for every run also isolates the brake check's empty
	// grant store, even when two jobs fire at the same instant.
	dir, err := os.MkdirTemp(root, "digest-"+now.Format("20060102-150405")+"-")
	if err != nil {
		return result, err
	}
	result.Directory = dir
	var summary strings.Builder
	metadata := io.MultiWriter(w, &summary)
	for _, scene := range []struct {
		kind  Scenario
		fetch func(context.Context, time.Time) (string, error)
	}{
		{EmailSummary, opts.Source.EmailSummary},
		{CalendarReminder, opts.Source.CalendarReminder},
	} {
		decision := policy.Decide(Input{
			Profile: opts.Profile, Scenario: scene.kind, Now: now,
			RememberLater: opts.RememberLater, ProposedAction: nil, Notify: false,
		})
		if _, err := fmt.Fprintf(metadata, "%s: %s (%s)\n", scene.kind, decision.Decision, decision.Reason); err != nil {
			return result, err
		}
		switch decision.Decision {
		case Summarize, NoteOnly:
			body, err := scene.fetch(ctx, now)
			if err != nil {
				return result, fmt.Errorf("digest %s: %w", scene.kind, err)
			}
			path := filepath.Join(dir, string(scene.kind)+".txt")
			if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
				return result, err
			}
			label := "Local digest"
			if decision.Decision == NoteOnly {
				label = "Local candidate (not inserted into memory)"
			}
			if _, err := fmt.Fprintf(metadata, "%s: %s\n", label, path); err != nil {
				return result, err
			}
			if decision.Decision == Summarize {
				if _, err := fmt.Fprintln(w, body); err != nil {
					return result, err
				}
			}
		}
	}
	approver := standingauth.Approver{
		Store: standingauth.New(filepath.Join(dir, "standing-auth")),
		Inner: agent.UnattendedApprover{},
	}
	if err := checkDigestBrakes(ctx, policy.Decide, approver, now, metadata); err != nil {
		return result, err
	}
	if _, err := fmt.Fprintln(metadata, "Brakes held: digest output local only; no model; no outbound; no send/draft/create/notify; no tools executed."); err != nil {
		return result, err
	}
	result.Summary = strings.TrimSpace(summary.String())
	return result, nil
}

func checkDigestBrakes(ctx context.Context, decide func(Input) Result, approver agent.Approver, now time.Time, w io.Writer) error {
	tool := tools.GmailSendTool{DraftByDefault: true}
	for _, raw := range []string{
		`{"to":"demo@example.com","subject":"Fake reply","body":"Thanks"}`,
		`{"to":"demo@example.com","subject":"Fake reply","body":"Thanks","as_draft":false}`,
	} {
		req, err := tool.ApprovalRequest(json.RawMessage(raw))
		if err != nil {
			return err
		}
		result := decide(Input{Profile: config.ProfilePersonal, Scenario: StandingAuthCheck, Now: now, ProposedAction: &req})
		decision, err := approver.Approve(ctx, req)
		if err != nil || decision.Allow || result.Decision != Quiet {
			return fmt.Errorf("brakes failed: policy=%s allow=%v error=%v", result.Decision, decision.Allow, err)
		}
		if _, err := fmt.Fprintf(w, "standing_auth_check: %s; %s denied without grant/human; brakes held.\n", result.Decision, req.Summary); err != nil {
			return err
		}
	}
	return nil
}
