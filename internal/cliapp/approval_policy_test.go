package cliapp

import (
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
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
