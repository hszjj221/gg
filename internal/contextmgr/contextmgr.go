package contextmgr

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

const toolResultPreviewRunes = 1200

type SummaryState struct {
	Text                string
	ThroughMessageCount int
}

type BuildInput struct {
	System  []agent.Message
	History []agent.Message
	Current agent.Message
	Summary SummaryState
	Config  config.ContextConfig
}

type BuildResult struct {
	Messages             []agent.Message
	PromptTokens         int
	KeptTurns            int
	SummaryIncluded      bool
	TruncatedToolResults bool
}

func Build(input BuildInput) BuildResult {
	cfg := input.Config
	messages := make([]agent.Message, 0, len(input.System)+len(input.History)+2)
	messages = append(messages, input.System...)
	if strings.TrimSpace(input.Summary.Text) != "" {
		messages = append(messages, SummarySystemMessage(input.Summary.Text))
	}
	start := input.Summary.ThroughMessageCount
	if start < 0 || start > len(input.History) {
		start = 0
	}
	messages = append(messages, cloneMessages(input.History[start:])...)
	messages = append(messages, input.Current)
	result := BuildResult{
		Messages:        messages,
		PromptTokens:    EstimateMessages(messages),
		KeptTurns:       CountUserTurns(input.History[start:]),
		SummaryIncluded: strings.TrimSpace(input.Summary.Text) != "",
	}
	if cfg.MaxPromptTokens <= 0 || result.PromptTokens <= cfg.MaxPromptTokens || !result.SummaryIncluded {
		return result
	}

	maxTailTurns := cfg.TailTurns
	if maxTailTurns <= 0 {
		maxTailTurns = 1
	}
	for turns := maxTailTurns; turns >= 1; turns-- {
		tailStart := TailStart(input.History, turns)
		if input.Summary.ThroughMessageCount > tailStart && input.Summary.ThroughMessageCount <= len(input.History) {
			tailStart = input.Summary.ThroughMessageCount
		}
		messages = make([]agent.Message, 0, len(input.System)+len(input.History[tailStart:])+2)
		messages = append(messages, input.System...)
		if strings.TrimSpace(input.Summary.Text) != "" {
			messages = append(messages, SummarySystemMessage(input.Summary.Text))
		}
		messages = append(messages, cloneMessages(input.History[tailStart:])...)
		messages = append(messages, input.Current)
		result.Messages = messages
		result.PromptTokens = EstimateMessages(messages)
		result.KeptTurns = CountUserTurns(input.History[tailStart:])
		if result.PromptTokens <= cfg.MaxPromptTokens {
			return result
		}
	}

	for i := range result.Messages {
		if result.Messages[i].Role != agent.RoleTool || len([]rune(result.Messages[i].Content)) <= toolResultPreviewRunes {
			continue
		}
		result.Messages[i].Content = truncateRunes(result.Messages[i].Content, toolResultPreviewRunes) + "\n... truncated for context ..."
		result.Messages[i].ContentBlocks = []agent.ContentBlock{{Type: agent.ContentText, Text: result.Messages[i].Content}}
		result.TruncatedToolResults = true
	}
	result.PromptTokens = EstimateMessages(result.Messages)
	return result
}

func SummarySystemMessage(summary string) agent.Message {
	return agent.Message{
		Role:    agent.RoleSystem,
		Content: "Conversation summary for earlier messages:\n" + strings.TrimSpace(summary),
	}
}

func SummarizePrefix(history []agent.Message, summary SummaryState, tailTurns int) ([]agent.Message, int, int) {
	tailStart := TailStart(history, tailTurns)
	if summary.ThroughMessageCount < 0 || summary.ThroughMessageCount > len(history) {
		summary.ThroughMessageCount = 0
	}
	if summary.ThroughMessageCount > tailStart {
		return nil, tailStart, CountUserTurns(history[tailStart:])
	}
	return cloneMessages(history[summary.ThroughMessageCount:tailStart]), tailStart, CountUserTurns(history[tailStart:])
}

func TailStart(messages []agent.Message, turns int) int {
	if turns <= 0 {
		turns = 1
	}
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != agent.RoleUser {
			continue
		}
		seen++
		if seen == turns {
			return i
		}
	}
	return 0
}

func CountUserTurns(messages []agent.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == agent.RoleUser {
			count++
		}
	}
	return count
}

func EstimateMessages(messages []agent.Message) int {
	total := 0
	for _, message := range messages {
		total += EstimateText(string(message.Role)) + EstimateText(message.Content)
		for _, block := range message.ContentBlocks {
			total += EstimateText(block.Text)
		}
		for _, call := range message.ToolCalls {
			total += EstimateText(call.ID) + EstimateText(call.Name) + EstimateText(string(call.Arguments))
		}
		total += EstimateText(message.ToolCallID) + EstimateText(message.ToolName)
	}
	return total
}

func EstimateText(text string) int {
	if text == "" {
		return 0
	}
	tokens := 0
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 0 {
			break
		}
		if r < utf8.RuneSelf {
			tokens++
		} else {
			tokens += 4
		}
		text = text[size:]
	}
	return (tokens + 3) / 4
}

func FormatSummaryPrompt(previous string, messages []agent.Message, maxTokens int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Summarize the following gg conversation history in at most %d tokens.\n", maxTokens)
	b.WriteString("Preserve user goals, decisions, file paths, tool results, unresolved tasks, and constraints.\n")
	b.WriteString("Do not invent facts. Write concise bullet points.\n")
	if strings.TrimSpace(previous) != "" {
		b.WriteString("\nExisting summary to carry forward:\n")
		b.WriteString(strings.TrimSpace(previous))
		b.WriteString("\n")
	}
	b.WriteString("\nMessages to add:\n")
	for _, message := range messages {
		fmt.Fprintf(&b, "\n[%s]", message.Role)
		if message.ToolName != "" {
			fmt.Fprintf(&b, " tool=%s", message.ToolName)
		}
		if message.ToolCallID != "" {
			fmt.Fprintf(&b, " tool_call_id=%s", message.ToolCallID)
		}
		if len(message.ToolCalls) > 0 {
			fmt.Fprintf(&b, " tool_calls=%d", len(message.ToolCalls))
		}
		b.WriteString("\n")
		content := message.Content
		if content == "" && len(message.ToolCalls) > 0 {
			content = formatToolCalls(message.ToolCalls)
		}
		b.WriteString(truncateRunes(content, 4000))
		b.WriteString("\n")
	}
	return b.String()
}

func cloneMessages(messages []agent.Message) []agent.Message {
	out := make([]agent.Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].ContentBlocks = append([]agent.ContentBlock(nil), messages[i].ContentBlocks...)
		out[i].ToolCalls = append([]agent.ToolCall(nil), messages[i].ToolCalls...)
	}
	return out
}

func formatToolCalls(calls []agent.ToolCall) string {
	var b strings.Builder
	for _, call := range calls {
		fmt.Fprintf(&b, "%s %s %s\n", call.ID, call.Name, string(call.Arguments))
	}
	return strings.TrimSpace(b.String())
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
