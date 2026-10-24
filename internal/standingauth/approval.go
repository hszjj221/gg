package standingauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

// Matches never infers effects from a quote, a goal, or missing metadata.
// Dangerous/confirm (and unknown risk classes) cannot be waived in P2.
func (g Grant) Matches(req agent.ApprovalRequest, now time.Time) bool {
	if g.Validate() != nil || now.Before(g.CreatedAt) || !now.Before(g.ExpiresAt) || !slices.Contains(g.Scope.Tools, req.ToolName) {
		return false
	}
	if req.Risk != "" && req.Risk != agent.ApprovalRiskReadOnly {
		return false
	}
	for _, dim := range []struct {
		name  string
		value *bool
	}{
		{"E", req.ExternallyVisible}, {"M", req.SpendsMoneyOrChangesPermissions}, {"U", req.ActsInUserName},
	} {
		if dim.value == nil || (*dim.value && !slices.Contains(g.Scope.Consequences, dim.name)) {
			return false
		}
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(req.Arguments, &args) != nil {
		return false
	}
	for key, want := range g.Scope.Arguments {
		var expected, actual any
		// Preserve numbers exactly; float64 rounding must not broaden a limit.
		wantDecoder, actualDecoder := json.NewDecoder(bytes.NewReader(want)), json.NewDecoder(bytes.NewReader(args[key]))
		wantDecoder.UseNumber()
		actualDecoder.UseNumber()
		if wantDecoder.Decode(&expected) != nil || actualDecoder.Decode(&actual) != nil || !reflect.DeepEqual(expected, actual) {
			return false
		}
	}
	return true
}

// Approver wraps the policy once in the shared turn executor, covering CLI,
// default, scheduled, and Telegram turns without changing their fallback rules.
type Approver struct {
	Inner agent.Approver
	Store *Store
}

func (a Approver) Approve(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	if err := ctx.Err(); err != nil {
		return agent.ApprovalDecision{}, err
	}
	if a.Store != nil {
		grants, err := a.Store.List()
		if err != nil {
			return agent.ApprovalDecision{}, err
		}
		for _, g := range grants {
			if g.Matches(req, time.Now()) {
				if err := a.Store.audit(g.ID, req); err != nil {
					return agent.ApprovalDecision{}, err
				}
				return agent.ApprovalDecision{Allow: true}, nil
			}
		}
	}
	inner := a.Inner
	if inner == nil {
		inner = agent.DefaultApprover{}
	}
	return inner.Approve(ctx, req)
}

type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	GrantID   string    `json:"grantId"`
	ToolName  string    `json:"toolName"`
	Summary   string    `json:"summary"`
	Decision  string    `json:"decision"`
}

func (s *Store) audit(id string, req agent.ApprovalRequest) error {
	data, err := json.Marshal(AuditEntry{time.Now().UTC(), id, req.ToolName, req.Summary, "allow-by-standing-grant"})
	if err != nil {
		return err
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "audit.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return errors.Join(err, f.Close())
	}
	// One append per record, as in scheduler run logs. Sync before allowing the
	// effect: failure to persist the authorization audit fails closed.
	data = append(data, '\n')
	n, writeErr := f.Write(data)
	if n != len(data) && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	return errors.Join(writeErr, f.Sync(), f.Close())
}
