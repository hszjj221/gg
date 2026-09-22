package session

import "errors"

var (
	ErrNotFound = errors.New("session not found")
	// ErrConflict means a Store snapshot is stale because another process has
	// advanced the same session. Callers should discard it and reopen by ID.
	ErrConflict = errors.New("session changed by another process")
	// ErrLocked means another process did not release the short-lived writer
	// lease within the wait budget.
	ErrLocked = errors.New("session is locked by another process")
)
