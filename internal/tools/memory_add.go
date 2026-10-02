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
	store memory.ToolStore
}

func NewMemoryAddTool(store memory.ToolStore) MemoryAddTool {
	return MemoryAddTool{store: store}
}

func (t MemoryAddTool) Name() string { return "memory_add" }

func (t MemoryAddTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "memory_add",
		Description: "Append a concise memory to the user's structured memory. Scope selects the destination: general (MEMORY.md, stable preferences and durable facts), daily (today's log, things that happened today), person:<name> (facts about a person), group:<name> (facts about a group). Target selects the layer: workspace (default) — this project's memory, searched first and shadowing the global layer on conflicts; global — the cross-project store, for explicitly cross-project memory. Layer guidance: project-specific facts, preferences and people go to workspace; cross-project identity, long-term preferences and people relevant everywhere go to global. When unsure, keep the default workspace: a misplaced workspace entry is easy to correct, while a misplaced global entry affects every project. Without a workspace context, target is ignored and everything lands in the global store. Use only for stable user preferences, durable facts, or repeated rules. Do not save temporary tasks, guesses, API keys, tokens, passwords, or sensitive personal data.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{"type": "string"},
				"scope":   map[string]any{"type": "string", "description": "general (default), daily, person:<name>, or group:<name>"},
				"target":  map[string]any{"type": "string", "description": "memory layer to write to", "enum": []string{"workspace", "global"}},
			},
			"required": []string{"content"},
		},
	}
}

func (t MemoryAddTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Content string `json:"content"`
		Scope   string `json:"scope"`
		Target  string `json:"target"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid memory_add arguments: %w", err))
	}
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		scope = "general"
	}
	target := strings.ToLower(strings.TrimSpace(input.Target))
	var toGlobal bool
	switch target {
	case "", "workspace":
		toGlobal = false
	case "global":
		toGlobal = true
	default:
		return errorResult(fmt.Errorf("unknown memory target %q: want workspace or global", input.Target))
	}
	// Resolve the write functions up front: on a plain (global-only)
	// store the Global variants are identical to the plain ones, so
	// target is naturally ignored without a workspace context.
	appendCurated := t.store.AppendCurated
	appendDaily := t.store.AppendDaily
	appendPerson := t.store.AppendPerson
	appendGroup := t.store.AppendGroup
	if toGlobal {
		appendCurated = t.store.AppendCuratedGlobal
		appendDaily = t.store.AppendDailyGlobal
		appendPerson = t.store.AppendPersonGlobal
		appendGroup = t.store.AppendGroupGlobal
	}
	var err error
	switch {
	case scope == "general":
		err = appendCurated(input.Content)
	case scope == "daily":
		err = appendDaily(input.Content)
	case strings.HasPrefix(scope, "person:"):
		name := strings.TrimSpace(strings.TrimPrefix(scope, "person:"))
		if name == "" {
			err = fmt.Errorf("person scope needs a name, e.g. \"person:Zhang San\"")
		} else {
			err = appendPerson(name, input.Content)
		}
	case strings.HasPrefix(scope, "group:"):
		name := strings.TrimSpace(strings.TrimPrefix(scope, "group:"))
		if name == "" {
			err = fmt.Errorf("group scope needs a name, e.g. \"group:running club\"")
		} else {
			err = appendGroup(name, input.Content)
		}
	default:
		err = fmt.Errorf("unknown memory scope %q: want general, daily, person:<name>, or group:<name>", scope)
	}
	if err != nil {
		return errorResult(err)
	}
	return textResult("memory added")
}
