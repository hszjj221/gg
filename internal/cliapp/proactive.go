package cliapp

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/proactive"
	"github.com/hszjj221/gg/internal/standingauth"
	"github.com/hszjj221/gg/internal/tools"
)

const proactiveUsage = "usage: gg --profile personal proactive demo [--quiet-hours HH:MM-HH:MM] [--at HH:MM] [--remember-later]"

func runProactiveCommand(ctx context.Context, cfg config.Config, args cli.Args, stdout, stderr io.Writer) int {
	if len(args.ProactiveArgs) == 0 || args.ProactiveArgs[0] != "demo" {
		fmt.Fprintln(stderr, proactiveUsage)
		return 2
	}
	fs := flag.NewFlagSet("gg proactive demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policy := proactive.Policy{}
	fs.StringVar(&policy.QuietHours, "quiet-hours", "", "HH:MM-HH:MM local quiet window (default: disabled)")
	at := fs.String("at", "10:00", "fake local time HH:MM (default: 10:00)")
	later := fs.Bool("remember-later", false, "capture fake digests as local note candidates")
	if err := fs.Parse(args.ProactiveArgs[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, proactiveUsage)
		return 2
	}
	now, err := time.ParseInLocation("15:04", *at, time.Local)
	if err != nil || now.Format("15:04") != *at {
		fmt.Fprintln(stderr, "--at must be HH:MM")
		return 2
	}
	if err := policy.Validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if cfg.Profile != config.ProfilePersonal {
		fmt.Fprintln(stdout, "Quiet: proactivity disabled; use --profile personal for the fake demo.")
		return 0
	}
	// Isolate from real grants, memory, sessions, and connectors. Only fake
	// candidate notes are retained; this path never constructs a live client.
	root := filepath.Join(cfg.HomeDir, ".gg", "proactive")
	if err := os.MkdirAll(root, 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dir, err := os.MkdirTemp(root, "demo-")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Fake personal demo at %s; OS notify off (max 0/run).\nDemo directory: %s\n", *at, dir)
	for _, scene := range []struct {
		kind proactive.Scenario
		text string
	}{
		{proactive.EmailSummary, "Fake inbox: Alex sent the planning notes; Sam confirmed lunch. Two messages, no reply needed."},
		{proactive.CalendarReminder, "Fake agenda: planning starts at " + now.Add(30*time.Minute).Format("15:04") + ". Read-only reminder; no event created."},
	} {
		result := policy.Decide(proactive.Input{Profile: cfg.Profile, Scenario: scene.kind, Now: now, RememberLater: *later})
		fmt.Fprintf(stdout, "%s: %s (%s)\n", scene.kind, result.Decision, result.Reason)
		switch result.Decision {
		case proactive.Summarize:
			fmt.Fprintln(stdout, scene.text)
		case proactive.NoteOnly:
			path := filepath.Join(dir, string(scene.kind)+".txt")
			if err := os.WriteFile(path, []byte(scene.text+"\n"), 0o600); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintln(stdout, "Local candidate (not inserted into memory):", path)
		}
	}
	// Use real tool metadata and the same standing-auth + CLI approver as
	// runtime turns, with no human and a fresh empty store. Never Execute.
	approver := standingauth.Approver{
		Store: standingauth.New(filepath.Join(dir, "standing-auth")),
		Inner: newPolicyApprover(args, nil, io.Discard, false),
	}
	tool := tools.GmailSendTool{DraftByDefault: true}
	for _, raw := range []string{
		`{"to":"demo@example.com","subject":"Fake reply","body":"Thanks"}`,
		`{"to":"demo@example.com","subject":"Fake reply","body":"Thanks","as_draft":false}`,
	} {
		req, err := tool.ApprovalRequest(json.RawMessage(raw))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		result := policy.Decide(proactive.Input{Profile: cfg.Profile, Scenario: proactive.StandingAuthCheck, Now: now, ProposedAction: &req})
		decision, err := approver.Approve(ctx, req)
		if err != nil || decision.Allow || result.Decision != proactive.Quiet {
			fmt.Fprintf(stderr, "brakes failed: policy=%s allow=%v error=%v\n", result.Decision, decision.Allow, err)
			return 1
		}
		fmt.Fprintf(stdout, "standing_auth_check: %s; %s denied without grant/human; brakes held.\n", result.Decision, req.Summary)
	}
	fmt.Fprintln(stdout, "Brakes held: digests stayed local; no Gmail send/draft, calendar_create, memory_add, or notify executed.")
	return 0
}
