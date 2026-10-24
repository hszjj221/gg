package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Risk is an optional classification from the tool.
	// ApprovalRiskReadOnly may run without a prompt.
	// ApprovalRiskDangerous always needs a human when one can be asked,
	// and is denied when nobody can answer — including under --yolo.
	// ApprovalRiskConfirm also needs a human (a model ask, or a risk
	// check that failed closed). It is denied when nobody can answer,
	// including under --yolo, but it is not a classifier denylist hit.
	// Empty means the ordinary approval policy for that tool.
	Risk string `json:"risk,omitempty"`
	// RiskReason explains Risk. Approvers show it with the request.
	RiskReason string `json:"riskReason,omitempty"`
	// Consequence pointers distinguish unknown (nil/omitted) from an explicit
	// false. Unknown is not evidence of safety or authorization; requests
	// without asserted E/M/U retain the existing Risk-based policy.
	// Reversible (JSON "reversible", R) means the whole effect can be undone
	// cheaply and locally, without external parties noticing.
	Reversible *bool `json:"reversible,omitempty"`
	// ExternallyVisible (JSON "externallyVisible", E) includes traces visible
	// to service operators, even when nothing is public.
	ExternallyVisible *bool `json:"externallyVisible,omitempty"`
	// SpendsMoneyOrChangesPermissions (JSON "spendsMoneyOrChangesPermissions",
	// M) marks cost beyond normal chat or a change in authority/access.
	SpendsMoneyOrChangesPermissions *bool `json:"spendsMoneyOrChangesPermissions,omitempty"`
	// ActsInUserName (JSON "actsInUserName", U) marks creating or sending
	// something attributable to the user, including in a private account.
	ActsInUserName *bool `json:"actsInUserName,omitempty"`
}

// RequiresHumanConfirm reports asserted E/M/U consequences, independently
// of Risk. False does not grant permission; the existing risk and approval
// mode rules still apply, including when consequences are unknown.
func (r ApprovalRequest) RequiresHumanConfirm() bool {
	return (r.ExternallyVisible != nil && *r.ExternallyVisible) ||
		(r.SpendsMoneyOrChangesPermissions != nil && *r.SpendsMoneyOrChangesPermissions) ||
		(r.ActsInUserName != nil && *r.ActsInUserName)
}

const (
	// ApprovalRiskReadOnly marks a call the policy may run without a prompt,
	// such as a clearly read-only shell command.
	ApprovalRiskReadOnly = "readonly"
	// ApprovalRiskDangerous marks a call that must not be auto-approved.
	// Interactive sessions still ask; --yolo and unattended allow-all deny it.
	ApprovalRiskDangerous = "dangerous"
	// ApprovalRiskConfirm marks a call the risk check will not auto-run
	// (model score above the ask threshold, or a failed check). Interactive
	// sessions ask; --yolo and unattended allow-all do not skip it.
	ApprovalRiskConfirm = "confirm"
)

type ApprovalDecision struct {
	Allow bool `json:"allow"`
	// Reason, when set on a denial, is the tool error shown to the model
	// and the user. Empty keeps the generic "denied by user" message.
	Reason string `json:"reason,omitempty"`
}

type Approver interface {
	Approve(context.Context, ApprovalRequest) (ApprovalDecision, error)
}

// DefaultApprover is the policy used when a caller supplies no approver.
// It denies asserted E/M/U consequences and dangerous/confirm risks;
// ordinary risk-free and read-only calls keep running,
// and an unrecognized risk is denied rather than assumed safe.
// A nil approver must therefore not mean "allow everything".
type DefaultApprover struct{}

func (DefaultApprover) Approve(_ context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	if req.RequiresHumanConfirm() {
		return ApprovalDecision{Reason: fmt.Sprintf("tool call %q needs human confirmation for external visibility, spending/permission changes, or acting in the user's name, but no approver is configured", req.ToolName)}, nil
	}
	switch req.Risk {
	case "", ApprovalRiskReadOnly:
		return ApprovalDecision{Allow: true}, nil
	case ApprovalRiskDangerous:
		return ApprovalDecision{Allow: false, Reason: "dangerous command denied: no approver is configured"}, nil
	case ApprovalRiskConfirm:
		return ApprovalDecision{Allow: false, Reason: "command needs confirmation but no approver is configured"}, nil
	default:
		return ApprovalDecision{Allow: false, Reason: fmt.Sprintf("unknown approval risk %q denied", req.Risk)}, nil
	}
}

// UnattendedApprover is the approval policy for runs where no human is
// present to answer prompts: scheduled jobs, daemon jobs, and chat bots. It
// allows clearly read-only calls, denies ordinary calls unless AllowAll was
// opted into, and always denies asserted E/M/U and dangerous/confirm calls —
// even with AllowAll.
type UnattendedApprover struct {
	AllowAll bool
}

// Approve implements Approver. Asserted E/M/U consequences, dangerous
// commands, and risk-check confirmations are denied even with --allow-all,
// because nobody is present to approve them.
func (a UnattendedApprover) Approve(_ context.Context, req ApprovalRequest) (ApprovalDecision, error) {
	if req.RequiresHumanConfirm() {
		return ApprovalDecision{Reason: fmt.Sprintf("tool call %q needs human confirmation for external visibility, spending/permission changes, or acting in the user's name; denied unattended, including with --allow-all", req.ToolName)}, nil
	}
	if req.Risk == ApprovalRiskReadOnly {
		return ApprovalDecision{Allow: true}, nil
	}
	if req.Risk == ApprovalRiskDangerous {
		why := req.RiskReason
		if why == "" {
			why = "dangerous command"
		}
		return ApprovalDecision{
			Allow:  false,
			Reason: fmt.Sprintf("tool call %q denied: %s. Dangerous commands are not run unattended, including with --allow-all.", req.ToolName, why),
		}, nil
	}
	if req.Risk == ApprovalRiskConfirm {
		why := req.RiskReason
		if why == "" {
			why = "confirmation required"
		}
		return ApprovalDecision{
			Allow:  false,
			Reason: fmt.Sprintf("tool call %q denied: %s. Commands that need confirmation are not run unattended, including with --allow-all.", req.ToolName, why),
		}, nil
	}
	return ApprovalDecision{Allow: a.AllowAll}, nil
}

type ApprovalDescriber interface {
	ApprovalRequest(json.RawMessage) (ApprovalRequest, error)
}

// ParallelTool opts into concurrent execution. Only tools whose operations
// are independent and safe to overlap should implement it. Other tools run
// in call order, with a barrier before and after execution.
type ParallelTool interface {
	ParallelSafe() bool
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
