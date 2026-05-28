package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hszjj221/gg/internal/agent"
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
		Description: "Create or overwrite a text file within the current working directory, creating parent directories as needed.",
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

func (t WriteTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid write arguments: %w", err))
	}
	path, err := resolveInsideCWD(t.cwd, input.Path)
	if err != nil {
		return errorResult(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errorResult(err)
	}
	if err := os.WriteFile(path, []byte(input.Content), 0o644); err != nil {
		return errorResult(err)
	}
	return textResult(fmt.Sprintf("wrote %d bytes to %s", len(input.Content), input.Path))
}
