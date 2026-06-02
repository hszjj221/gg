package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/memory"
)

type MemoryAddTool struct {
	path string
}

func NewMemoryAddTool(path string) MemoryAddTool {
	return MemoryAddTool{path: path}
}

func (t MemoryAddTool) Name() string { return "memory_add" }

func (t MemoryAddTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "memory_add",
		Description: "Append a concise long-term memory to ~/.gg/memory.md. Use only for stable user preferences, durable facts, or repeated rules. Do not save temporary tasks, guesses, API keys, tokens, passwords, or sensitive personal data.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"content"},
		},
	}
}

func (t MemoryAddTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid memory_add arguments: %w", err))
	}
	if err := memory.Append(t.path, input.Content); err != nil {
		return errorResult(err)
	}
	return textResult("memory added")
}
