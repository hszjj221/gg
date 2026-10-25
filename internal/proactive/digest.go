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

type DigestOptions struct {
	Profile       string
	QuietHours    string
	Now           time.Time // zero uses the local wall clock
	RememberLater bool
	HomeDir       string
	Writer        io.Writer // nil suppresses stdout, including digest bodies
}

type DigestResult struct {
	Directory string
	Summary   string // decisions and paths only, suitable for scheduler history
}

// RunDigest produces local stub inbox/agenda digests. It never constructs a
// model or connector client, executes a tool, or loads real standing grants.
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
		kind Scenario
		text string
	}{
		{EmailSummary, "Stub inbox: Alex sent the planning notes; Sam confirmed lunch. Two fake messages, no reply needed."},
		{CalendarReminder, "Stub agenda: fake planning starts at " + now.Add(30*time.Minute).Format("15:04") + ". Read-only reminder; no event created."},
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
			path := filepath.Join(dir, string(scene.kind)+".txt")
			if err := os.WriteFile(path, []byte(scene.text+"\n"), 0o600); err != nil {
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
				if _, err := fmt.Fprintln(w, scene.text); err != nil {
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
	if _, err := fmt.Fprintln(metadata, "Brakes held: local only; no model; no outbound; no tools executed."); err != nil {
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
