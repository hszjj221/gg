package cliapp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/cli"
)

// policyApprover is the CLI approval policy, including terminal availability.
type policyApprover struct {
	mode     string
	terminal bool
	reader   *bufio.Reader
	stderr   io.Writer
}

func newPolicyApprover(args cli.Args, reader *bufio.Reader, stderr io.Writer, terminal bool) agent.Approver {
	mode := args.Approval
	if mode == "" {
		mode = "auto"
	}
	return policyApprover{mode: mode, terminal: terminal, reader: reader, stderr: stderr}
}

func (a policyApprover) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	action, reason := approvalActionFor(a.mode, a.terminal, req)
	switch action {
	case approvalAllow:
		return agent.ApprovalDecision{Allow: true}, nil
	case approvalPrompt:
		return a.prompt(req)
	default:
		fmt.Fprintln(a.stderr, reason)
		return agent.ApprovalDecision{Allow: false, Reason: reason}, nil
	}
}

func (a policyApprover) prompt(req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	fmt.Fprintf(a.stderr, "\nApprove tool call: %s\n", req.ToolName)
	if req.Summary != "" {
		fmt.Fprintf(a.stderr, "%s\n", req.Summary)
	}
	if req.Details != "" {
		fmt.Fprintf(a.stderr, "%s\n", req.Details)
	}
	fmt.Fprint(a.stderr, "Approve? [y/N] ")
	answer, err := a.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return agent.ApprovalDecision{}, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return agent.ApprovalDecision{Allow: answer == "y" || answer == "yes"}, nil
}

type approvalAction int

const (
	approvalAllow approvalAction = iota
	approvalPrompt
	approvalDeny
)

// approvalActionFor decides one request.
//
// Asserted E/M/U consequences require a human, including under yolo.
// Otherwise, read-only shell commands run without a prompt in every mode.
// Dangerous shell commands, and commands the risk check refused to
// auto-run, are prompted when a terminal can ask and the mode is not
// "never"; otherwise they are denied, including under yolo.
// Other approval-gated tools are prompted in a terminal for auto and
// on-request, allowed without a prompt for yolo and never, and denied
// when auto has no terminal.
func approvalActionFor(mode string, terminal bool, req agent.ApprovalRequest) (approvalAction, string) {
	if req.RequiresHumanConfirm() {
		if terminal && mode != "never" {
			return approvalPrompt, ""
		}
		return approvalDeny, fmt.Sprintf("tool call %q denied: external visibility, spending/permission changes, or acting in the user's name requires human confirmation. Run in a terminal without --approval never and approve the prompt to allow this once.", req.ToolName)
	}
	if req.Risk == agent.ApprovalRiskReadOnly {
		return approvalAllow, ""
	}
	if req.Risk == agent.ApprovalRiskDangerous {
		if terminal && mode != "never" {
			return approvalPrompt, ""
		}
		return approvalDeny, denyDangerous(req)
	}
	if req.Risk == agent.ApprovalRiskConfirm {
		if terminal && mode != "never" {
			return approvalPrompt, ""
		}
		return approvalDeny, denyConfirm(req)
	}
	switch mode {
	case "yolo", "never":
		return approvalAllow, ""
	default:
		if terminal {
			return approvalPrompt, ""
		}
		return approvalDeny, denyNonInteractive(req)
	}
}

func denyNonInteractive(req agent.ApprovalRequest) string {
	name := req.ToolName
	if name == "" {
		name = "tool"
	}
	return fmt.Sprintf("tool call %q denied: not running in a terminal, so --approval auto will not run approval-gated tools such as bash, edit, and write. Re-run in a terminal, use --approval on-request in a terminal, or pass --yolo to allow them without prompting.", name)
}

func denyDangerous(req agent.ApprovalRequest) string {
	name := req.ToolName
	if name == "" {
		name = "tool"
	}
	why := req.RiskReason
	if why == "" {
		why = "dangerous command"
	}
	return fmt.Sprintf("tool call %q denied: %s. Dangerous commands always need an interactive approval and stay blocked under --yolo when there is no terminal. Run in a terminal without --approval never and approve the prompt to allow this once.", name, why)
}

func denyConfirm(req agent.ApprovalRequest) string {
	name := req.ToolName
	if name == "" {
		name = "tool"
	}
	why := req.RiskReason
	if why == "" {
		why = "the risk check requires confirmation"
	}
	return fmt.Sprintf("tool call %q denied: %s. This command needs an interactive confirmation and stays blocked under --yolo when there is no terminal. Run in a terminal without --approval never and approve the prompt to allow this once.", name, why)
}
