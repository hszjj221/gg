package agent

import (
	"context"
	"encoding/json"
	"strings"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ContentType string

const (
	ContentText ContentType = "text"
)

type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonError     StopReason = "error"
	StopReasonMaxTokens StopReason = "max_tokens"
	StopReasonCanceled  StopReason = "canceled"
)

type EventType string

const (
	EventTextDelta EventType = "text_delta"
	// EventThinkingDelta carries a fragment of the model's internal
	// reasoning. Providers surface this differently (OpenAI-compatible
	// reasoning_content, Anthropic thinking blocks); protocol adapters
	// normalize them into this event. Thinking is ephemeral stream
	// display: it is not part of the assistant message content and is
	// not persisted in transcripts.
	EventThinkingDelta  EventType = "thinking_delta"
	EventToolCallStart  EventType = "tool_call_start"
	EventToolCallFinish EventType = "tool_call_finish"
	EventUserMessage    EventType = "user_message"
)

type Usage struct {
	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
	TotalTokens      int `json:"totalTokens,omitempty"`
}

func (u Usage) Add(other Usage) Usage {
	return Usage{
		PromptTokens:     u.PromptTokens + other.PromptTokens,
		CompletionTokens: u.CompletionTokens + other.CompletionTokens,
		TotalTokens:      u.TotalTokens + other.TotalTokens,
	}
}

func (u Usage) IsZero() bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0
}

type ContentBlock struct {
	Type ContentType `json:"type"`
	Text string      `json:"text,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// Reasoning carries the model's internal reasoning (e.g. the
	// OpenAI-compatible reasoning_content field) so protocol adapters can
	// echo it back when the provider requires it for tool-call
	// continuation. It is transient: never part of Content and never
	// persisted in transcripts.
	Reasoning     string         `json:"-"`
	ContentBlocks []ContentBlock `json:"contentBlocks,omitempty"`
	ToolCalls     []ToolCall     `json:"toolCalls,omitempty"`
	ToolCallID    string         `json:"toolCallId,omitempty"`
	ToolName      string         `json:"toolName,omitempty"`
	Timestamp     int64          `json:"timestamp,omitempty"`
	StopReason    StopReason     `json:"stopReason,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// Content and ContentBlocks may mirror each other in older sessions.
func MessageText(message Message) string {
	text := message.Content
	var parts []string
	if text == "" {
		for _, block := range message.ContentBlocks {
			if block.Type == ContentText {
				parts = append(parts, block.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	if message.Role == RoleAssistant && message.Error != "" {
		text += "\n[Response incomplete: " + message.Error + "]"
	}
	return text
}

type AssistantMessage struct {
	Message
	StopReason StopReason `json:"stopReason"`
	Error      string     `json:"error,omitempty"`
	Usage      Usage      `json:"usage,omitempty"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError"`
	Usage   Usage          `json:"usage,omitempty"`
}

type ApprovalRequest struct {
	ToolName  string          `json:"toolName"`
	Summary   string          `json:"summary"`
	Details   string          `json:"details,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// PreApproved marks a request the tool itself pre-approved, so the
	// runner skips the approver. Only set for narrow, path-scoped
	// exemptions the tool registry explicitly allows (currently:
	// write/edit targeting the workspace's agent area). Reads and every
	// other tool keep the normal approval policy.
	PreApproved bool `json:"preApproved,omitempty"`
	// PreApprovedReason is a short human-readable reason for the
	// pre-approval, e.g. "agent area". Kept visible for transparency.
	PreApprovedReason string `json:"preApprovedReason,omitempty"`
}

type ApprovalDecision struct {
	Allow bool `json:"allow"`
}

type Approver interface {
	Approve(context.Context, ApprovalRequest) (ApprovalDecision, error)
}

type ApprovalDescriber interface {
	ApprovalRequest(json.RawMessage) (ApprovalRequest, error)
}

type Tool interface {
	Name() string
	Definition() ToolDefinition
	Execute(context.Context, json.RawMessage) ToolResult
}

type Request struct {
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	MaxOutputTokens int              `json:"maxOutputTokens,omitempty"`
}

type Event struct {
	Type       EventType `json:"type"`
	Text       string    `json:"text,omitempty"`
	ToolCallID string    `json:"toolCallId,omitempty"`
	ToolName   string    `json:"toolName,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Details    string    `json:"details,omitempty"`
	IsError    bool      `json:"isError,omitempty"`
}

type Provider interface {
	Complete(context.Context, Request, func(Event)) (AssistantMessage, error)
}
