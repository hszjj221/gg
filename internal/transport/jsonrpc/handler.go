package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hszjj221/gg/internal/app"
)

const Version = "2.0"

// ProtocolVersion versions gg's method, result, event, and application-error
// contract independently from the JSON-RPC wire version.
// 1.2: run.start requireApproval defaults to true (fail-closed); clients
// that omit it must handle approval_requested events.
// 1.3: system.info results gain degradedProviders (omitted when healthy),
// listing tool providers whose build failed and degraded to absent.
const ProtocolVersion = "1.3"

// capabilities lists transport-level features. Tool capabilities are
// derived from app.ToolCapabilities so the advertised set cannot drift
// from the tools actually registered.
var capabilities = append([]string{
	"run.approval",
	"run.event-replay",
	"run.reattach",
	"run.steering",
	"session.clone",
	"session.fork",
	"session.tree",
}, app.ToolCapabilities()...)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *ErrorData `json:"data,omitempty"`
}

type ErrorData struct {
	Code      app.ErrorCode `json:"code"`
	Retryable bool          `json:"retryable"`
}

type SystemInfo struct {
	ProtocolVersion string   `json:"protocolVersion"`
	Capabilities    []string `json:"capabilities"`
	// DegradedProviders names tool capabilities whose providers failed
	// their most recent build across open sessions, with reasons.
	// Omitted when every session built its full toolset.
	DegradedProviders []app.DegradedProvider `json:"degradedProviders,omitempty"`
}

type Handler struct {
	rt         *app.Runtime
	runContext context.Context
}

func NewHandler(rt *app.Runtime) *Handler {
	return NewHandlerWithContext(context.Background(), rt)
}

func NewHandlerWithContext(ctx context.Context, rt *app.Runtime) *Handler {
	return &Handler{rt: rt, runContext: ctx}
}

func (h *Handler) Handle(ctx context.Context, request Request) Response {
	response := Response{JSONRPC: Version, ID: request.ID}
	if request.JSONRPC != "" && request.JSONRPC != Version {
		response.Error = &Error{Code: -32600, Message: "invalid JSON-RPC version"}
		return response
	}
	if strings.TrimSpace(request.Method) == "" {
		response.Error = &Error{Code: -32600, Message: "method is required"}
		return response
	}
	result, err := h.call(ctx, request.Method, request.Params)
	if err != nil {
		if rpcErr, ok := err.(*Error); ok {
			response.Error = rpcErr
		} else {
			response.Error = ErrorFrom(err)
		}
		return response
	}
	response.Result = result
	return response
}

func (e *Error) Error() string { return e.Message }

func (h *Handler) call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "system.info":
		return SystemInfo{
			ProtocolVersion:   ProtocolVersion,
			Capabilities:      append([]string(nil), capabilities...),
			DegradedProviders: h.rt.DegradedProviders(),
		}, nil
	case "session.list":
		return h.rt.ListSessions()
	case "session.create":
		var params struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		return h.rt.CreateSession(params.Name)
	case "session.open", "session.get":
		var params sessionParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.SessionID == "" {
			return nil, invalidParams("sessionId is required")
		}
		if method == "session.open" {
			return h.rt.OpenSession(params.SessionID)
		}
		return h.rt.Snapshot(params.SessionID)
	case "session.rename":
		var params struct {
			SessionID string `json:"sessionId"`
			Name      string `json:"name"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.SessionID == "" {
			return nil, invalidParams("sessionId is required")
		}
		return h.rt.RenameSession(params.SessionID, params.Name)
	case "session.action":
		var params struct {
			SessionID string            `json:"sessionId"`
			Action    app.SessionAction `json:"action"`
			NodeID    string            `json:"nodeId"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.SessionID == "" || params.Action == "" {
			return nil, invalidParams("sessionId and action are required")
		}
		return h.rt.SessionAction(params.SessionID, params.Action, params.NodeID)
	case "run.start":
		var params struct {
			SessionID string `json:"sessionId"`
			Prompt    string `json:"prompt"`
			// RequireApproval is a pointer so "absent" is distinguishable
			// from "explicitly false": a client that forgets the field must
			// not silently disable the approval pipeline (fail closed).
			// Pass explicit false only from trusted automation.
			RequireApproval *bool `json:"requireApproval"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.SessionID == "" || strings.TrimSpace(params.Prompt) == "" {
			return nil, invalidParams("sessionId and prompt are required")
		}
		requireApproval := true
		if params.RequireApproval != nil {
			requireApproval = *params.RequireApproval
		}
		run, err := h.rt.StartTurn(h.runContext, params.SessionID, params.Prompt, requireApproval)
		if err != nil {
			return nil, err
		}
		return map[string]string{"runId": run.ID()}, nil
	case "run.wait":
		var params struct {
			RunID         string `json:"runId"`
			AfterSequence int64  `json:"afterSequence"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.RunID == "" {
			return nil, invalidParams("runId is required")
		}
		events, done, err := h.rt.WaitRun(ctx, params.RunID, params.AfterSequence)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": events, "done": done}, nil
	case "run.get":
		var params runParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.RunID == "" {
			return nil, invalidParams("runId is required")
		}
		return h.rt.RunStatus(params.RunID)
	case "run.active":
		var params sessionParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.SessionID == "" {
			return nil, invalidParams("sessionId is required")
		}
		return h.rt.ActiveRun(params.SessionID)
	case "run.cancel":
		var params runParams
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.RunID == "" {
			return nil, invalidParams("runId is required")
		}
		return okResult(), h.rt.CancelRun(params.RunID)
	case "run.approve":
		var params struct {
			RunID      string `json:"runId"`
			ApprovalID string `json:"approvalId"`
			Allow      bool   `json:"allow"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.RunID == "" || params.ApprovalID == "" {
			return nil, invalidParams("runId and approvalId are required")
		}
		return okResult(), h.rt.Approve(params.RunID, params.ApprovalID, params.Allow)
	case "run.steer":
		var params struct {
			SessionID string `json:"sessionId"`
			Text      string `json:"text"`
			FollowUp  bool   `json:"followUp"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.SessionID == "" || strings.TrimSpace(params.Text) == "" {
			return nil, invalidParams("sessionId and text are required")
		}
		return okResult(), h.rt.Steer(params.SessionID, params.Text, params.FollowUp)
	case "artifact.list":
		return h.rt.ListArtifacts()
	case "artifact.get":
		var params struct {
			ArtifactID string `json:"artifactId"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.ArtifactID == "" {
			return nil, invalidParams("artifactId is required")
		}
		return h.rt.GetArtifact(params.ArtifactID)
	case "artifact.publish":
		var params struct {
			ArtifactID string `json:"artifactId"`
		}
		if err := decodeParams(raw, &params); err != nil {
			return nil, err
		}
		if params.ArtifactID == "" {
			return nil, invalidParams("artifactId is required")
		}
		return h.rt.PublishArtifact(params.ArtifactID)
	default:
		return nil, &Error{Code: -32601, Message: fmt.Sprintf("method %q not found", method)}
	}
}

type sessionParams struct {
	SessionID string `json:"sessionId"`
}

type runParams struct {
	RunID string `json:"runId"`
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte(`{}`)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return invalidParams(err.Error())
	}
	return nil
}

func invalidParams(message string) *Error {
	return &Error{Code: -32602, Message: message}
}

func okResult() map[string]bool {
	return map[string]bool{"ok": true}
}

// ErrorFrom maps application errors to stable JSON-RPC numeric and symbolic
// codes. Other transports reuse this shape for consistent error handling.
func ErrorFrom(err error) *Error {
	var appErr *app.AppError
	if !errors.As(err, &appErr) {
		return &Error{Code: -32000, Message: err.Error()}
	}
	numeric := map[app.ErrorCode]int{
		app.ErrorSessionNotFound:     -32001,
		app.ErrorRunNotFound:         -32002,
		app.ErrorRunConflict:         -32003,
		app.ErrorApprovalExpired:     -32004,
		app.ErrorEventHistoryExpired: -32005,
		app.ErrorSessionConflict:     -32006,
		app.ErrorInvalidAction:       -32007,
	}[appErr.Code]
	if numeric == 0 {
		numeric = -32000
	}
	return &Error{
		Code:    numeric,
		Message: appErr.Error(),
		Data:    &ErrorData{Code: appErr.Code, Retryable: appErr.Retryable},
	}
}
