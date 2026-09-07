package contextmgr

import (
	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"strings"
	"testing"
)

func TestDoNotSilentlyDropUnsummarizedConstraint(t *testing.T) {
	result := Build(BuildInput{History: []agent.Message{
		{Role: agent.RoleUser, Content: "old task"}, {Role: agent.RoleAssistant, Content: "old answer"},
		{Role: agent.RoleUser, Content: "REQUIRED_CONSTRAINT " + strings.Repeat("x", 4000)}, {Role: agent.RoleAssistant, Content: "understood"},
		{Role: agent.RoleUser, Content: "latest"}, {Role: agent.RoleAssistant, Content: "ok"},
	}, Current: agent.Message{Role: agent.RoleUser, Content: "continue"}, Summary: SummaryState{Text: "summary of old task only", ThroughMessageCount: 2}, Config: config.ContextConfig{MaxPromptTokens: 100, TailTurns: 1, AutoCompact: true}})
	if !containsContent(result.Messages, "REQUIRED_CONSTRAINT") && result.PromptTokens <= 100 {
		t.Fatalf("unsummarized constraint dropped, returned %d tokens so app will skip compaction", result.PromptTokens)
	}
}
func TestDoNotCountMirroredContentTwice(t *testing.T) {
	msg := agent.Message{Role: agent.RoleAssistant, Content: strings.Repeat("x", 400)}
	want := EstimateMessages([]agent.Message{msg})
	msg.ContentBlocks = []agent.ContentBlock{{Type: agent.ContentText, Text: msg.Content}}
	got := EstimateMessages([]agent.Message{msg})
	if got != want {
		t.Fatalf("same serialized content estimated as %d vs %d tokens", want, got)
	}
}
