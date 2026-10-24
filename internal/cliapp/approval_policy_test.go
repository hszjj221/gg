package cliapp

import (
	"bufio"
	"context"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/tools"
)

func TestApprovalActionFor(t *testing.T) {
	bash := agent.ApprovalRequest{ToolName: "bash"}
	readOnly := agent.ApprovalRequest{ToolName: "bash", Risk: agent.ApprovalRiskReadOnly, RiskReason: "read-only command"}
	dangerous := agent.ApprovalRequest{ToolName: "bash", Risk: agent.ApprovalRiskDangerous, RiskReason: "rm -rf targets /"}

	cases := []struct {
		name     string
		mode     string
		terminal bool
		req      agent.ApprovalRequest
		want     approvalAction
		reason   string
	}{
		{name: "auto terminal prompts", mode: "auto", terminal: true, req: bash, want: approvalPrompt},
		{name: "auto non-terminal denies", mode: "auto", terminal: false, req: bash, want: approvalDeny, reason: "--yolo"},
		{name: "auto non-terminal allows read-only", mode: "auto", terminal: false, req: readOnly, want: approvalAllow},
		{name: "auto terminal allows read-only", mode: "auto", terminal: true, req: readOnly, want: approvalAllow},
		{name: "yolo allows", mode: "yolo", terminal: false, req: bash, want: approvalAllow},
		{name: "yolo prompts dangerous in a terminal", mode: "yolo", terminal: true, req: dangerous, want: approvalPrompt},
		{name: "yolo denies dangerous without a terminal", mode: "yolo", terminal: false, req: dangerous, want: approvalDeny, reason: "rm -rf targets /"},
		{name: "never allows", mode: "never", terminal: false, req: bash, want: approvalAllow},
		{name: "never denies dangerous", mode: "never", terminal: true, req: dangerous, want: approvalDeny, reason: "rm -rf"},
		{name: "on-request prompts", mode: "on-request", terminal: true, req: bash, want: approvalPrompt},
		{name: "yolo prompts confirm in a terminal", mode: "yolo", terminal: true, req: agent.ApprovalRequest{ToolName: "bash", Risk: agent.ApprovalRiskConfirm, RiskReason: "jev noul 0.9 > 0.7; confirmation required"}, want: approvalPrompt},
		{name: "yolo denies confirm without a terminal", mode: "yolo", terminal: false, req: agent.ApprovalRequest{ToolName: "bash", Risk: agent.ApprovalRiskConfirm, RiskReason: "jev noul 0.9 > 0.7; confirmation required"}, want: approvalDeny, reason: "confirmation required"},
		{name: "never denies confirm", mode: "never", terminal: true, req: agent.ApprovalRequest{ToolName: "bash", Risk: agent.ApprovalRiskConfirm, RiskReason: "timed out"}, want: approvalDeny, reason: "timed out"},
		{name: "auto denies confirm without a terminal", mode: "auto", terminal: false, req: agent.ApprovalRequest{ToolName: "bash", Risk: agent.ApprovalRiskConfirm, RiskReason: "timed out"}, want: approvalDeny, reason: "timed out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := approvalActionFor(tc.mode, tc.terminal, tc.req)
			if got != tc.want {
				t.Fatalf("action=%v want %v (%s)", got, tc.want, reason)
			}
			if tc.reason != "" && !strings.Contains(reason, tc.reason) {
				t.Fatalf("reason %q does not contain %q", reason, tc.reason)
			}
		})
	}
}

func TestPersonalExplicitSendNeedsApproval(t *testing.T) {
	mail := tools.NewGmailSendTool(nil)
	mail.DraftByDefault = true
	req, err := mail.ApprovalRequest([]byte(`{"to":"bob@example.com","subject":"hi","body":"hello","as_draft":false}`))
	if err != nil || !req.RequiresHumanConfirm() || !strings.Contains(req.Summary, "send email") {
		t.Fatalf("explicit send approval=%+v, err=%v", req, err)
	}
	for _, mode := range []string{"auto", "on-request", "never"} {
		for _, terminal := range []bool{false, true} {
			var output strings.Builder
			a := newPolicyApprover(cli.Args{Profile: "personal", Approval: mode}, bufio.NewReader(strings.NewReader("no\n")), &output, terminal)
			decision, err := a.Approve(context.Background(), req)
			if err != nil || decision.Allow {
				t.Fatalf("mode=%s terminal=%v decision=%+v err=%v", mode, terminal, decision, err)
			}
			if prompted := strings.Contains(output.String(), "Approve? [y/N]"); prompted != (terminal && mode != "never") {
				t.Fatalf("mode=%s terminal=%v output=%s", mode, terminal, output.String())
			}
		}
	}
}

func TestConsequenceApprovalModes(t *testing.T) {
	for _, req := range []agent.ApprovalRequest{
		{ExternallyVisible: new(true)},
		{SpendsMoneyOrChangesPermissions: new(true)},
		{ActsInUserName: new(true)},
	} {
		for _, risk := range []string{"", agent.ApprovalRiskReadOnly} {
			req.Risk = risk
			for _, mode := range []string{"auto", "on-request", "yolo", "never"} {
				for _, terminal := range []bool{false, true} {
					got, reason := approvalActionFor(mode, terminal, req)
					want := approvalDeny
					if terminal && mode != "never" {
						want = approvalPrompt
					}
					if got != want || (want == approvalDeny && !strings.Contains(reason, "human confirmation")) {
						t.Fatalf("mode %q terminal %v risk %q: got %v (%s), want %v", mode, terminal, risk, got, reason, want)
					}
				}
			}
		}
	}
	for _, answer := range []string{"yes\n", "no\n"} {
		var output strings.Builder
		a := policyApprover{mode: "yolo", terminal: true, reader: bufio.NewReader(strings.NewReader(answer)), stderr: &output}
		got, err := a.Approve(context.Background(), agent.ApprovalRequest{ExternallyVisible: new(true)})
		if err != nil || got.Allow != (answer == "yes\n") || !strings.Contains(output.String(), "Approve? [y/N]") {
			t.Fatalf("answer %q: %+v, %v; output %s", answer, got, err, output.String())
		}
	}
}
