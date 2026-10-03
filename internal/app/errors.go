package app

import "fmt"

// ErrorCode is a stable, transport-neutral application error identifier.
// Transports may map it to their native error representation without parsing
// human-readable messages.
type ErrorCode string

const (
	ErrorSessionNotFound     ErrorCode = "session_not_found"
	ErrorRunNotFound         ErrorCode = "run_not_found"
	ErrorRunConflict         ErrorCode = "run_conflict"
	ErrorRuntimeClosed       ErrorCode = "runtime_closed"
	ErrorRunCapacity         ErrorCode = "run_capacity"
	ErrorApprovalExpired     ErrorCode = "approval_expired"
	ErrorEventHistoryExpired ErrorCode = "event_history_expired"
	ErrorSessionConflict     ErrorCode = "session_conflict"
	ErrorInvalidAction       ErrorCode = "invalid_action"
	ErrorArtifactNotFound    ErrorCode = "artifact_not_found"
	// ErrorWorkspaceMismatch reports a session bound to a different
	// workspace than the one requested. Messages name the workspace only;
	// roots stay private to the daemon host.
	ErrorWorkspaceMismatch ErrorCode = "workspace_mismatch"
)

// AppError carries a stable code while retaining the original cause for
// errors.Is/errors.As checks inside the application.
type AppError struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Cause     error
}

func (e *AppError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *AppError) Unwrap() error { return e.Cause }

func errorf(code ErrorCode, retryable bool, format string, args ...any) error {
	return &AppError{Code: code, Message: fmt.Sprintf(format, args...), Retryable: retryable}
}

func wrapError(code ErrorCode, retryable bool, cause error, format string, args ...any) error {
	return &AppError{Code: code, Message: fmt.Sprintf(format, args...), Retryable: retryable, Cause: cause}
}
