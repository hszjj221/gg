package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/standingauth"
	"github.com/hszjj221/gg/internal/tools"
)

// Reuse the real Gmail approval metadata, with a fake execution (no network).
type standingMailTool struct {
	runs int
	risk string
}

func (*standingMailTool) Name() string { return "gmail_send" }
func (t *standingMailTool) Definition() agent.ToolDefinition {
	return tools.NewGmailSendTool(nil).Definition()
}
func (t *standingMailTool) ApprovalRequest(args json.RawMessage) (agent.ApprovalRequest, error) {
	req, err := tools.NewGmailSendTool(nil).ApprovalRequest(args)
	req.Risk = t.risk
	return req, err
}
func (t *standingMailTool) Execute(context.Context, json.RawMessage) agent.ToolResult {
	t.runs++
	return agent.ToolResult{Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "fake draft created"}}}
}

func TestSharedRuntimeStandingAuthorization(t *testing.T) {
	for _, mode := range []string{"interactive", "default", "telegram", "job allow-all"} {
		for _, state := range []string{"hit", "miss", "dangerous", "confirm"} {
			t.Run(mode+"/"+state, func(t *testing.T) {
				dir := t.TempDir()
				store := standingauth.New(standingauth.DefaultDir(dir))
				g, err := store.Add(standingauth.Grant{
					Subject:   "gg on behalf of alice@example.com",
					Scope:     standingauth.Scope{Tools: []string{"gmail_send"}, Consequences: []string{"E", "U"}, Arguments: map[string]json.RawMessage{"to": json.RawMessage(`"bob@example.com"`), "as_draft": json.RawMessage(`true`)}},
					ExpiresAt: time.Now().Add(time.Hour), SourceQuote: "gg may create drafts for bob@example.com as alice@example.com for an hour.",
				})
				if err != nil {
					t.Fatal(err)
				}
				tool := &standingMailTool{}
				args := json.RawMessage(`{"to":"bob@example.com","as_draft":true,"subject":"hi","body":"hello"}`)
				if state == "miss" {
					args = json.RawMessage(`{"to":"eve@example.com","as_draft":true,"subject":"hi","body":"hello"}`)
				}
				if state == "dangerous" || state == "confirm" {
					tool.risk = state
				}
				calls := 0
				provider := diagnosticProviderFunc(func(context.Context, agent.Request, func(agent.Event)) (agent.AssistantMessage, error) {
					calls++
					if calls == 1 {
						return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "mail", Name: tool.Name(), Arguments: args}}}, StopReason: agent.StopReasonToolUse}, nil
					}
					return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "done"}, StopReason: agent.StopReasonEndTurn}, nil
				})
				svc := NewService(Options{Config: config.Config{CWD: dir, HomeDir: dir, Selection: "test:model"},
					ProviderFactory: func(config.Config) agent.Provider { return provider },
					ToolProviders:   []ToolProvider{{Build: func(context.Context, ToolContext) ([]agent.Tool, error) { return []agent.Tool{tool}, nil }}},
				})
				t.Cleanup(func() {
					if err := svc.Close(); err != nil {
						t.Error(err)
					}
				})
				human := &recordingApprover{}
				var approver agent.Approver
				switch mode {
				case "interactive":
					approver = human
				case "telegram":
					approver = agent.UnattendedApprover{}
				case "job allow-all":
					approver = agent.UnattendedApprover{AllowAll: true}
				}
				if _, err := svc.Run(context.Background(), "create a draft", nil, approver); err != nil {
					t.Fatal(err)
				}
				wantRuns := 0
				if state == "hit" {
					wantRuns = 1
				}
				if tool.runs != wantRuns {
					t.Fatalf("tool runs=%d, want %d", tool.runs, wantRuns)
				}
				if mode == "interactive" {
					wantPrompts := 1
					if state == "hit" {
						wantPrompts = 0
					}
					if got := len(human.consulted()); got != wantPrompts {
						t.Fatalf("human prompts=%d, want %d", got, wantPrompts)
					}
				}
				data, err := os.ReadFile(filepath.Join(standingauth.DefaultDir(dir), "audit.jsonl"))
				if state == "hit" {
					var entry standingauth.AuditEntry
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(data, &entry); err != nil {
						t.Fatal(err)
					}
					if entry.GrantID != g.ID || entry.Decision != "allow-by-standing-grant" {
						t.Fatalf("audit=%+v", entry)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("non-hit wrote audit: %s, %v", data, err)
				}
			})
		}
	}
}
