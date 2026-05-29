package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

func (t ReadTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
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
	data, err := os.ReadFile(path)
	if err != nil {
		return errorResult(err)
	}
	if len(data) > defaultMaxReadBytes {
		data = data[:defaultMaxReadBytes]
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	offset := input.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := input.Limit
	if limit <= 0 || limit > defaultMaxReadLines {
		limit = defaultMaxReadLines
	}
	start := offset - 1
	if start >= len(lines) {
		return textResult("")
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	return textResult(strings.Join(lines[start:end], "\n"))
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

func resolveExistingInsideRoot(root, path, rootLabel string) (string, error) {
	target, err := resolveInsideRoot(root, path, rootLabel)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q is outside %s", path, rootLabel)
	}
	return realTarget, nil
}
