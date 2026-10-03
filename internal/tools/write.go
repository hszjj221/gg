package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/workspace"
)

type WriteTool struct {
	cwd string
}

func NewWriteTool(cwd string) WriteTool {
	return WriteTool{cwd: cwd}
}

func (t WriteTool) Name() string { return "write" }

func (t WriteTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "write",
		Description: "Create or overwrite a text file within the current working directory, creating parent directories as needed. Prefer writing drafts, downloads, and intermediate files to `.gg/agent/` — that directory is the agent's private area and writes there skip the approval prompt.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t WriteTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid write arguments: %w", err)
	}
	path, err := resolveWritableInsideCWD(t.cwd, input.Path)
	if err != nil {
		return agent.ApprovalRequest{}, err
	}
	operation := "create"
	before := ""
	if data, err := os.ReadFile(path); err == nil {
		operation = "overwrite"
		before = string(data)
	} else if !os.IsNotExist(err) {
		return agent.ApprovalRequest{}, err
	}
	details := fmt.Sprintf("path: %s\noperation: %s\ncontent bytes: %d\n\n%s", input.Path, operation, len(input.Content), contentChangePreview(before, input.Content))
	req := agent.ApprovalRequest{
		ToolName:  "write",
		Summary:   fmt.Sprintf("write %s %s (%d bytes)", operation, input.Path, len(input.Content)),
		Details:   details,
		Arguments: raw,
	}
	// Writes into the workspace's agent area are pre-approved: that
	// directory is the agent's private area, so the agent writing its own
	// files there needs no user consent. Everything else keeps the
	// normal approval policy. The check is symlink-aware (InRealAgentDir):
	// a symlinked agent area never grants the exemption.
	if workspace.InRealAgentDir(t.cwd, path) {
		req.PreApproved = true
		req.PreApprovedReason = "agent area"
	}
	return req, nil
}

func (t WriteTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	if err := ctx.Err(); err != nil {
		return errorResult(err)
	}
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid write arguments: %w", err))
	}
	path, err := resolveWritableInsideCWD(t.cwd, input.Path)
	if err != nil {
		return errorResult(err)
	}
	// Lazily create the agent area on first use; writes there are
	// pre-approved, so the directory must exist before the tool runs.
	// Fail closed: if the agent area is a symlink (or became one after
	// approval), refuse the write instead of letting it land outside the
	// real agent area without the approval those paths would need.
	if workspace.InAgentDir(t.cwd, path) {
		if _, err := workspace.EnsureRealAgentDir(t.cwd); err != nil {
			return errorResult(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errorResult(err)
	}
	unlock, err := lockFile(ctx, path)
	if err != nil {
		return errorResult(err)
	}
	defer unlock()
	if err := writeFileAtomic(ctx, path, []byte(input.Content)); err != nil {
		return errorResult(err)
	}
	return textResult(fmt.Sprintf("wrote %d bytes to %s", len(input.Content), input.Path))
}
