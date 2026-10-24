package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestApprovalConsequencesJSON(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"reversible":false,"externallyVisible":false,"spendsMoneyOrChangesPermissions":false,"actsInUserName":false}`,
		`{"reversible":true,"externallyVisible":true,"spendsMoneyOrChangesPermissions":true,"actsInUserName":true}`,
	} {
		var req ApprovalRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		var want map[string]*bool
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		got := map[string]*bool{
			"reversible": req.Reversible, "externallyVisible": req.ExternallyVisible,
			"spendsMoneyOrChangesPermissions": req.SpendsMoneyOrChangesPermissions, "actsInUserName": req.ActsInUserName,
		}
		for key, value := range got {
			if !reflect.DeepEqual(value, want[key]) {
				t.Fatalf("%s: %s = %v, want %v", raw, key, value, want[key])
			}
		}
		encoded, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		for key, value := range got {
			if value == nil {
				if _, exists := fields[key]; exists {
					t.Fatalf("unknown %s must be omitted: %s", key, encoded)
				}
			} else if string(fields[key]) != strconv.FormatBool(*value) {
				t.Fatalf("explicit %s lost: %s", key, encoded)
			}
		}
	}
}

func TestApproversRouteConsequencesBeforeRisk(t *testing.T) {
	cases := []struct {
		name    string
		req     ApprovalRequest
		confirm bool
	}{
		{"unknown", ApprovalRequest{}, false},
		{"local reversible", ApprovalRequest{Reversible: new(true), ExternallyVisible: new(false), SpendsMoneyOrChangesPermissions: new(false), ActsInUserName: new(false)}, false},
		{"irreversible alone", ApprovalRequest{Reversible: new(false)}, false},
		{"external", ApprovalRequest{ExternallyVisible: new(true)}, true},
		{"money or permissions", ApprovalRequest{SpendsMoneyOrChangesPermissions: new(true)}, true},
		{"user attribution", ApprovalRequest{ActsInUserName: new(true)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.req.RequiresHumanConfirm() != tc.confirm {
				t.Fatalf("RequiresHumanConfirm = %v, want %v", tc.req.RequiresHumanConfirm(), tc.confirm)
			}
			for _, risk := range []string{"", ApprovalRiskReadOnly, ApprovalRiskDangerous, ApprovalRiskConfirm} {
				req := tc.req
				req.ToolName, req.Risk = "arbitrary_tool", risk
				for _, policy := range []struct {
					approver Approver
					ordinary bool
				}{
					{DefaultApprover{}, true},
					{UnattendedApprover{}, false},
					{UnattendedApprover{AllowAll: true}, true},
				} {
					got, err := policy.approver.Approve(context.Background(), req)
					want := !tc.confirm && (risk == ApprovalRiskReadOnly || (risk == "" && policy.ordinary))
					if err != nil || got.Allow != want {
						t.Fatalf("%T %+v risk %q: decision = %+v, %v; want allow %v", policy.approver, policy.approver, risk, got, err, want)
					}
					if tc.confirm && !strings.Contains(got.Reason, "human confirmation") {
						t.Fatalf("missing confirmation reason: %+v", got)
					}
				}
			}
		})
	}
}

func TestRunnerConsequencesOverridePreApproval(t *testing.T) {
	for _, allow := range []bool{false, true} {
		tool := &fakeApprovalTool{name: "external", risk: ApprovalRiskReadOnly,
			request: ApprovalRequest{ExternallyVisible: new(true), PreApproved: true}}
		provider := &fakeProvider{responses: []AssistantMessage{
			toolUseMessage("c1", tool.Name(), `{}`),
			{Message: Message{Role: RoleAssistant, Content: "done"}, StopReason: StopReasonEndTurn},
		}}
		var approver Approver
		if allow {
			approver = &fakeApprover{allow: true}
		}
		runner := NewRunnerWithOptions(provider, []Tool{tool}, RunnerOptions{Approver: approver})
		if _, err := runner.Run(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		if (tool.runs == 1) != allow {
			t.Fatalf("human allow %v: runs = %d", allow, tool.runs)
		}
	}
}
