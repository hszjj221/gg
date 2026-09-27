package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/memory"
)

// MemorySearchTool searches the user's structured long-term memory.
type MemorySearchTool struct {
	store *memory.Store
}

func NewMemorySearchTool(store *memory.Store) MemorySearchTool {
	return MemorySearchTool{store: store}
}

func (t MemorySearchTool) Name() string { return "memory_search" }

func (t MemorySearchTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "memory_search",
		Description: "Search the user's long-term memory in ~/.gg/memory/ (curated facts, daily logs, people and group pages). Use it to recall past context the current conversation does not contain. Returns up to 10 matches as path:line: snippet.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "keyword query"},
				"scope": map[string]any{
					"type":        "string",
					"description": "limit to one memory area",
					"enum":        []string{"all", "curated", "daily", "people", "groups"},
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t MemorySearchTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid memory_search arguments: %w", err))
	}
	hits, err := t.store.Search(input.Query, input.Scope)
	if err != nil {
		return errorResult(err)
	}
	if len(hits) == 0 {
		return textResult("no memory matches")
	}
	var b strings.Builder
	for _, hit := range hits {
		fmt.Fprintf(&b, "%s:%d: %s\n", hit.Path, hit.Line, hit.Snippet)
	}
	return textResult(strings.TrimSpace(b.String()))
}
