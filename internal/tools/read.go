package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
)

type ReadTool struct {
	cwd string
}

func NewReadTool(cwd string) ReadTool {
	return ReadTool{cwd: cwd}
}

func (t ReadTool) Name() string { return "read" }

func (t ReadTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "read",
		Description: "Read a text file within the current working directory. Supports optional 1-indexed offset and line limit.",
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
	path, err := resolveInsideCWD(t.cwd, input.Path)
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
