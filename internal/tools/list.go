package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
)

type ListTool struct {
	cwd string
}

func NewListTool(cwd string) ListTool {
	return ListTool{cwd: cwd}
}

func (t ListTool) Name() string { return "list" }

func (t ListTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "list",
		Description: "List entries in a directory within the current working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "directory path, defaults to ."},
			},
		},
	}
}

func (t ListTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Path string `json:"path"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return errorResult(fmt.Errorf("invalid list arguments: %w", err))
		}
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = "."
	}
	path, err := resolveInsideCWD(t.cwd, input.Path)
	if err != nil {
		return errorResult(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return errorResult(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return textResult(strings.Join(names, "\n"))
}
