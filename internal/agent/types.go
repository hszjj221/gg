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
	EventTextDelta      EventType = "text_delta"
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
	Role          Role           `json:"role"`
	Content       string         `json:"content,omitempty"`
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
