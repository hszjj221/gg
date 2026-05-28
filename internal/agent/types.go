package agent

import (
	"context"
	"encoding/json"
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
	StopReasonEndTurn StopReason = "end_turn"
	StopReasonToolUse StopReason = "tool_use"
	StopReasonError   StopReason = "error"
)

type EventType string

const (
	EventTextDelta EventType = "text_delta"
)

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
}

type AssistantMessage struct {
	Message
	StopReason StopReason `json:"stopReason"`
	Error      string     `json:"error,omitempty"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

type Tool interface {
	Name() string
	Definition() ToolDefinition
	Execute(context.Context, json.RawMessage) ToolResult
}

type Request struct {
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

type Event struct {
	Type EventType `json:"type"`
	Text string    `json:"text,omitempty"`
}

type Provider interface {
	Complete(context.Context, Request, func(Event)) (AssistantMessage, error)
}
