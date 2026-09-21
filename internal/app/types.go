package app

import (
	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

// ProviderFactory creates a provider for the service's current model selection.
type ProviderFactory func(config.Config) agent.Provider

// Result is the UI-neutral result of submitting one prompt or application
// command to a conversation.
type Result struct {
	Content   string
	Usage     agent.Usage
	ModelName string
	TreeItems []TreeItem
}

type SessionAction string

const (
	SessionActionTree  SessionAction = "tree"
	SessionActionFork  SessionAction = "fork"
	SessionActionClone SessionAction = "clone"
)

// TreeItem is a transport-neutral projection of one visible message node.
// ParentID is the stable relationship; Depth is retained for the current TUI
// and can be ignored by richer clients that perform their own layout.
type TreeItem struct {
	ID       string     `json:"id"`
	ParentID *string    `json:"parentId,omitempty"`
	Depth    int        `json:"depth,omitempty"`
	Role     agent.Role `json:"role"`
	Text     string     `json:"text"`
	Active   bool       `json:"active"`
}

type SessionUpdate struct {
	Messages    []agent.Message `json:"messages"`
	TreeItems   []TreeItem      `json:"treeItems"`
	SessionID   string          `json:"sessionId"`
	SessionName string          `json:"sessionName"`
	ModelName   string          `json:"modelName"`
	SessionPath string          `json:"-"`
	Draft       string          `json:"draft,omitempty"`
	Notice      string          `json:"notice,omitempty"`
}

// Snapshot is an immutable view of the current conversation state.
type Snapshot struct {
	SessionID      string          `json:"sessionId"`
	SessionName    string          `json:"sessionName"`
	SessionPath    string          `json:"-"`
	ModelName      string          `json:"modelName"`
	Summary        string          `json:"summary,omitempty"`
	SummaryThrough int             `json:"summaryThrough,omitempty"`
	Messages       []agent.Message `json:"messages"`
	TreeItems      []TreeItem      `json:"treeItems"`
}
