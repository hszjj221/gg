package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

// recordingApprover records the approval requests it receives.
type recordingApprover struct {
	mu    sync.Mutex
	tools []string
	allow bool
}

func (a *recordingApprover) Approve(_ context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools = append(a.tools, req.ToolName)
	return agent.ApprovalDecision{Allow: a.allow}, nil
}

func (a *recordingApprover) consulted() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.tools...)
}

func unattendedTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	provider := &runtimeProvider{toolFirst: true}
	rt, err := NewRuntime(RuntimeOptions{
		Config:            config.Config{CWD: cwd, Selection: "test:model"},
		ProviderFactory:   func(config.Config) agent.Provider { return provider },
		WorkspaceRegistry: testRegistry(t),
		NoSkills:          true,
		Repository:        session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

// The injected approver must be consulted for approval-gated tools (bash);
// previously requireApproval=false meant a nil approver and tools executed
// without any policy check.
func TestStartTurnWithApproverConsultsInjectedApprover(t *testing.T) {
	rt := unattendedTestRuntime(t)
	snapshot, err := rt.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	approver := &recordingApprover{}
	run, err := rt.StartTurnWithApprover(context.Background(), snapshot.SessionID, "hi", approver)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range waitForRun(t, run, nil) {
		_ = event
	}
	consulted := approver.consulted()
	if len(consulted) != 1 || consulted[0] != "bash" {
		t.Fatalf("approver was consulted for %v, want [bash]", consulted)
	}
}

// A denied tool must not execute: the run should finish with the provider's
// follow-up response rather than the tool's output.
func TestStartTurnWithApproverDeniesToolCall(t *testing.T) {
	rt := unattendedTestRuntime(t)
	snapshot, err := rt.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	approver := &recordingApprover{allow: false}
	run, err := rt.StartTurnWithApprover(context.Background(), snapshot.SessionID, "hi", approver)
	if err != nil {
		t.Fatal(err)
	}
	events := waitForRun(t, run, nil)
	var toolResults []string
	for _, event := range events {
		if event.Type == EventAgent && event.Agent != nil && event.Agent.Type == agent.EventToolCallFinish {
			toolResults = append(toolResults, event.Agent.Details)
		}
	}
	if len(toolResults) != 1 || !strings.Contains(toolResults[0], "denied by user") {
		t.Fatalf("expected a denied tool result, got %v", toolResults)
	}
}
