package contextmgr

import (
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

func TestBuildUsesSummaryAndTailWithoutCuttingToolResults(t *testing.T) {
	history := []agent.Message{
		{Role: agent.RoleUser, Content: "old user"},
		{Role: agent.RoleAssistant, Content: "old assistant"},
		{Role: agent.RoleUser, Content: "keep user"},
		{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "read"}}},
		{Role: agent.RoleTool, ToolCallID: "call-1", ToolName: "read", Content: "tool output"},
		{Role: agent.RoleAssistant, Content: "keep assistant"},
	}

	result := Build(BuildInput{
		System:  []agent.Message{{Role: agent.RoleSystem, Content: "skills"}},
		History: history,
		Current: agent.Message{Role: agent.RoleUser, Content: "new task"},
		Summary: SummaryState{Text: "old summary", ThroughMessageCount: 2},
		Config:  config.ContextConfig{MaxPromptTokens: 1000, TailTurns: 1, SummaryMaxTokens: 100, AutoCompact: true},
	})

	if len(result.Messages) != 7 {
		t.Fatalf("unexpected context messages: %+v", result.Messages)
	}
	if result.Messages[1].Role != agent.RoleSystem || !strings.Contains(result.Messages[1].Content, "old summary") {
		t.Fatalf("summary system message missing: %+v", result.Messages)
	}
	if result.Messages[3].Role != agent.RoleAssistant || len(result.Messages[3].ToolCalls) != 1 {
		t.Fatalf("assistant tool call was cut: %+v", result.Messages)
	}
	if result.Messages[4].Role != agent.RoleTool || result.Messages[4].ToolCallID != "call-1" {
		t.Fatalf("tool result was cut: %+v", result.Messages)
	}
}

func TestSummarizePrefixKeepsLastUserTurns(t *testing.T) {
	history := []agent.Message{
		{Role: agent.RoleUser, Content: "u1"},
		{Role: agent.RoleAssistant, Content: "a1"},
		{Role: agent.RoleUser, Content: "u2"},
		{Role: agent.RoleAssistant, Content: "a2"},
		{Role: agent.RoleUser, Content: "u3"},
		{Role: agent.RoleAssistant, Content: "a3"},
	}

	messages, through, keptTurns := SummarizePrefix(history, SummaryState{}, 2)

	if through != 2 || keptTurns != 2 {
		t.Fatalf("unexpected through/kept: through=%d kept=%d", through, keptTurns)
	}
	if len(messages) != 2 || messages[0].Content != "u1" || messages[1].Content != "a1" {
		t.Fatalf("unexpected summarize prefix: %+v", messages)
	}
}

func TestBuildKeepsToolResultsForCompaction(t *testing.T) {
	longResult := strings.Repeat("x", 5000)
	history := []agent.Message{
		{Role: agent.RoleUser, Content: "old"},
		{Role: agent.RoleUser, Content: "inspect"},
		{Role: agent.RoleTool, ToolCallID: "call-1", ToolName: "read", Content: longResult},
	}

	result := Build(BuildInput{
		History: history,
		Current: agent.Message{Role: agent.RoleUser, Content: "new task"},
		Summary: SummaryState{Text: "older summary", ThroughMessageCount: 1},
		Config:  config.ContextConfig{MaxPromptTokens: 40, TailTurns: 1, SummaryMaxTokens: 100, AutoCompact: true},
	})

	if result.TruncatedToolResults || result.Messages[2].Content != longResult {
		t.Fatalf("unsummarized tool result must remain intact for compaction")
	}
	if result.PromptTokens <= 40 {
		t.Fatal("over-budget context must remain detectable")
	}

	if history[2].Content != longResult {
		t.Fatalf("original history was mutated")
	}
}

func TestBuildWithoutSummaryKeepsFullHistoryEvenOverBudget(t *testing.T) {
	history := []agent.Message{
		{Role: agent.RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: agent.RoleAssistant, Content: strings.Repeat("assistant ", 100)},
		{Role: agent.RoleUser, Content: "recent"},
	}

	result := Build(BuildInput{
		History: history,
		Current: agent.Message{Role: agent.RoleUser, Content: "new task"},
		Config:  config.ContextConfig{MaxPromptTokens: 1, TailTurns: 1, SummaryMaxTokens: 100, AutoCompact: true},
	})

	if len(result.Messages) != len(history)+1 {
		t.Fatalf("history should not be trimmed without summary: %+v", result.Messages)
	}
	if !containsContent(result.Messages, "old") {
		t.Fatalf("old history should remain available for auto compact detection: %+v", result.Messages)
	}
}

func TestBuildDoesNotDropUnsummarizedTurns(t *testing.T) {
	history := []agent.Message{
		{Role: agent.RoleUser, Content: strings.Repeat("u1 ", 80)},
		{Role: agent.RoleAssistant, Content: strings.Repeat("a1 ", 80)},
		{Role: agent.RoleUser, Content: strings.Repeat("u2 ", 80)},
		{Role: agent.RoleAssistant, Content: strings.Repeat("a2 ", 80)},
		{Role: agent.RoleUser, Content: "u3"},
		{Role: agent.RoleAssistant, Content: "a3"},
	}

	result := Build(BuildInput{
		History: history,
		Current: agent.Message{Role: agent.RoleUser, Content: "new task"},
		Summary: SummaryState{Text: "previous summary", ThroughMessageCount: 0},
		Config:  config.ContextConfig{MaxPromptTokens: 20, TailTurns: 3, SummaryMaxTokens: 100, AutoCompact: true},
	})

	if result.KeptTurns != 3 || !containsContent(result.Messages, "u1") || !containsContent(result.Messages, "u2") {
		t.Fatalf("unsummarized turns must not disappear: %+v", result)
	}
	if result.PromptTokens <= 20 {
		t.Fatal("compaction must still be triggered")
	}

}

func containsContent(messages []agent.Message, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}
