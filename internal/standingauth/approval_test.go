package standingauth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

func testRequest() agent.ApprovalRequest {
	return agent.ApprovalRequest{ToolName: "gmail_send", Summary: "save draft to bob@example.com",
		Arguments:  json.RawMessage(`{"to":"bob@example.com","as_draft":true,"subject":"hello","body":"hi"}`),
		Reversible: new(false), ExternallyVisible: new(true), SpendsMoneyOrChangesPermissions: new(false), ActsInUserName: new(true)}
}

func TestGrantMatching(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name  string
		grant func(*Grant)
		req   func(*agent.ApprovalRequest)
		hit   bool
	}{
		{name: "hit", hit: true},
		{name: "unrelated tool", req: func(r *agent.ApprovalRequest) { r.ToolName = "calendar_create" }},
		{name: "broader effects", req: func(r *agent.ApprovalRequest) { r.SpendsMoneyOrChangesPermissions = new(true) }},
		{name: "unknown effect", req: func(r *agent.ApprovalRequest) { r.SpendsMoneyOrChangesPermissions = nil }},
		{name: "narrow consequences", grant: func(g *Grant) { g.Scope.Consequences = []string{"E"} }},
		{name: "expired at boundary", grant: func(g *Grant) { g.ExpiresAt = now }},
		{name: "not started", grant: func(g *Grant) { g.CreatedAt = now.Add(time.Minute) }},
		{name: "recipient", req: func(r *agent.ApprovalRequest) {
			r.Arguments = json.RawMessage(`{"to":"eve@example.com","as_draft":true}`)
		}},
		{name: "send instead of draft", req: func(r *agent.ApprovalRequest) {
			r.Arguments = json.RawMessage(`{"to":"bob@example.com","as_draft":false}`)
		}},
		{name: "missing limit", req: func(r *agent.ApprovalRequest) { r.Arguments = json.RawMessage(`{"to":"bob@example.com"}`) }},
		{name: "malformed args", req: func(r *agent.ApprovalRequest) { r.Arguments = json.RawMessage(`{`) }},
		{name: "dangerous", req: func(r *agent.ApprovalRequest) { r.Risk = agent.ApprovalRiskDangerous }},
		{name: "confirm", req: func(r *agent.ApprovalRequest) { r.Risk = agent.ApprovalRiskConfirm }},
		{name: "unknown risk", req: func(r *agent.ApprovalRequest) { r.Risk = "future-risk" }},
		{name: "readonly with effects", hit: true, req: func(r *agent.ApprovalRequest) { r.Risk = agent.ApprovalRiskReadOnly }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, req := testGrant(), testRequest()
			g.CreatedAt = now.Add(-time.Minute)
			if tc.grant != nil {
				tc.grant(&g)
			}
			if tc.req != nil {
				tc.req(&req)
			}
			if got := g.Matches(req, now); got != tc.hit {
				t.Fatalf("hit=%v, want %v", got, tc.hit)
			}
		})
	}
	// Distinct integer limits must not collapse through float64 rounding.
	g, req := testGrant(), testRequest()
	g.Scope.Arguments = map[string]json.RawMessage{"id": json.RawMessage(`9007199254740992`)}
	req.Arguments = json.RawMessage(`{"id":9007199254740993}`)
	if g.Matches(req, now) {
		t.Fatal("rounded integer broadened scope")
	}
}

type recordingApprover struct{ calls int }

func (a *recordingApprover) Approve(context.Context, agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	a.calls++
	return agent.ApprovalDecision{Reason: "fallback"}, nil
}

func TestApproverHitAuditAndFallback(t *testing.T) {
	store := New(t.TempDir())
	g, err := store.Add(testGrant())
	if err != nil {
		t.Fatal(err)
	}
	inner := &recordingApprover{}
	a := Approver{Inner: inner, Store: store}
	decision, err := a.Approve(context.Background(), testRequest())
	if err != nil || !decision.Allow || inner.calls != 0 {
		t.Fatalf("hit: %+v, %v, fallback calls=%d", decision, err, inner.calls)
	}
	path := filepath.Join(store.dir, "audit.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry AuditEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.GrantID != g.ID || entry.ToolName != "gmail_send" || entry.Summary == "" || entry.Timestamp.IsZero() || entry.Decision != "allow-by-standing-grant" {
		t.Fatalf("audit: %+v", entry)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("audit permissions: %v, %v", info, err)
	}
	req := testRequest()
	req.ToolName = "unrelated"
	decision, err = a.Approve(context.Background(), req)
	if err != nil || decision.Allow || decision.Reason != "fallback" || inner.calls != 1 {
		t.Fatalf("miss: %+v, %v, calls=%d", decision, err, inner.calls)
	}
	if err := store.Revoke(g.ID); err != nil {
		t.Fatal(err)
	}
	decision, err = a.Approve(context.Background(), testRequest())
	if err != nil || decision.Allow || inner.calls != 2 {
		t.Fatalf("revoked: %+v, %v", decision, err)
	}
}

func TestApproverFailsClosedOnAuditOrStoreError(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		store := New(t.TempDir())
		g, err := store.Add(testGrant())
		if err != nil {
			t.Fatal(err)
		}
		if corrupt {
			if err := os.WriteFile(filepath.Join(store.dir, g.ID+".json"), []byte(`{`), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Mkdir(filepath.Join(store.dir, "audit.jsonl"), 0o700); err != nil {
			t.Fatal(err)
		}
		inner := &recordingApprover{}
		decision, err := (Approver{Inner: inner, Store: store}).Approve(context.Background(), testRequest())
		if err == nil || decision.Allow || inner.calls != 0 {
			t.Fatalf("error allowed request: %+v, %v", decision, err)
		}
	}
}
