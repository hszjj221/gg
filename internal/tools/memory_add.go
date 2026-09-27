package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/memory"
)

// MemoryAddTool appends to the user's structured long-term memory.
type MemoryAddTool struct {
	store *memory.Store
}

func NewMemoryAddTool(store *memory.Store) MemoryAddTool {
	return MemoryAddTool{store: store}
}

func (t MemoryAddTool) Name() string { return "memory_add" }

func (t MemoryAddTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "memory_add",
		Description: "Append a concise memory to the user's structured memory in ~/.gg/memory/. Scope selects the destination: general (MEMORY.md, stable preferences and durable facts), daily (today's log, things that happened today), person:<name> (facts about a person), group:<name> (facts about a group). Use only for stable user preferences, durable facts, or repeated rules. Do not save temporary tasks, guesses, API keys, tokens, passwords, or sensitive personal data.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{"type": "string"},
				"scope":   map[string]any{"type": "string", "description": "general (default), daily, person:<name>, or group:<name>"},
			},
			"required": []string{"content"},
		},
	}
}

func (t MemoryAddTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Content string `json:"content"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid memory_add arguments: %w", err))
	}
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		scope = "general"
	}
	var err error
	switch {
	case scope == "general":
		err = t.store.AppendCurated(input.Content)
	case scope == "daily":
		err = t.store.AppendDaily(input.Content)
	case strings.HasPrefix(scope, "person:"):
		name := strings.TrimSpace(strings.TrimPrefix(scope, "person:"))
		if name == "" {
			err = fmt.Errorf("person scope needs a name, e.g. \"person:Zhang San\"")
		} else {
			err = t.store.AppendPerson(name, input.Content)
		}
	case strings.HasPrefix(scope, "group:"):
		name := strings.TrimSpace(strings.TrimPrefix(scope, "group:"))
		if name == "" {
			err = fmt.Errorf("group scope needs a name, e.g. \"group:running club\"")
		} else {
			err = t.store.AppendGroup(name, input.Content)
		}
	default:
		err = fmt.Errorf("unknown memory scope %q: want general, daily, person:<name>, or group:<name>", scope)
	}
	if err != nil {
		return errorResult(err)
	}
	return textResult("memory added")
}
