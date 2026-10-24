package tools

import (
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func assertToolConsequences(t *testing.T, req agent.ApprovalRequest, r, e, m, u *bool) {
	t.Helper()
	for _, dim := range []struct {
		name string
		got  *bool
		want *bool
	}{
		{"Reversible", req.Reversible, r},
		{"ExternallyVisible", req.ExternallyVisible, e},
		{"SpendsMoneyOrChangesPermissions", req.SpendsMoneyOrChangesPermissions, m},
		{"ActsInUserName", req.ActsInUserName, u},
	} {
		if dim.want == nil {
			if dim.got != nil {
				t.Errorf("%s = %v, want unknown", dim.name, *dim.got)
			}
		} else if dim.got == nil {
			t.Errorf("%s is unknown, want %v", dim.name, *dim.want)
		} else if *dim.got != *dim.want {
			t.Errorf("%s = %v, want %v", dim.name, *dim.got, *dim.want)
		}
	}
}
