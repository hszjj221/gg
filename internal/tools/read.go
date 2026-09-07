package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/hszjj221/gg/internal/agent"
)

type ReadTool struct {
	cwd        string
	extraRoots []string
}

func NewReadTool(cwd string) ReadTool {
	return NewReadToolWithOptions(cwd, ReadOptions{})
}

type ReadOptions struct {
	ExtraRoots []string
}

func NewReadToolWithOptions(cwd string, options ReadOptions) ReadTool {
	return ReadTool{cwd: cwd, extraRoots: append([]string(nil), options.ExtraRoots...)}
}

func (t ReadTool) Name() string { return "read" }

func (t ReadTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "read",
		Description: "Read a text file within the current working directory or configured read-only skill directories. Supports optional 1-indexed offset and line limit.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"offset": map[string]any{"type": "integer"},
				"limit":  map[string]any{"type": "integer"},
			},
			"required": []string{"path"},
		},
	}
}

func (t ReadTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid read arguments: %w", err))
	}
	path, err := t.resolve(input.Path)
	if err != nil {
		return errorResult(err)
	}
	offset := input.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := input.Limit
	if limit <= 0 || limit > defaultMaxReadLines {
		limit = defaultMaxReadLines
	}
	output, err := readTextRange(ctx, path, offset, limit)
	if err != nil {
		return errorResult(fmt.Errorf("%w: %s", err, input.Path))
	}
	return textResult(output)
}

func (t ReadTool) resolve(path string) (string, error) {
	target, err := resolveExistingInsideRoot(t.cwd, path, "working directory")
	if err == nil {
		return target, nil
	}
	cwdErr := err
	if !filepath.IsAbs(path) {
		return "", cwdErr
	}
	for _, root := range t.extraRoots {
		target, err := resolveExistingInsideRoot(root, path, "configured read-only root")
		if err == nil {
			return target, nil
		}
	}
	return "", cwdErr
}
