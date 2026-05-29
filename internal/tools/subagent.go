package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
)

const (
	defaultSubagentMaxTurns  = 6
	absoluteSubagentMaxTurns = 12
)

type SubagentOptions struct {
	DefaultMaxTurns int
	MaxTurns        int
}

type SubagentTool struct {
	cwd      string
	provider agent.Provider
	options  SubagentOptions
}

func NewSubagentTool(cwd string, provider agent.Provider, options SubagentOptions) SubagentTool {
	if options.DefaultMaxTurns <= 0 {
		options.DefaultMaxTurns = defaultSubagentMaxTurns
	}
	if options.MaxTurns <= 0 || options.MaxTurns > absoluteSubagentMaxTurns {
		options.MaxTurns = absoluteSubagentMaxTurns
	}
	return SubagentTool{cwd: cwd, provider: provider, options: options}
}

func (t SubagentTool) Name() string { return "subagent" }

func (t SubagentTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "subagent",
		Description: "Run a synchronous read-only subagent for focused codebase research. The subagent can read, list, and grep only.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":     map[string]any{"type": "string"},
				"context":  map[string]any{"type": "string"},
				"maxTurns": map[string]any{"type": "integer"},
			},
			"required": []string{"task"},
		},
	}
}

func (t SubagentTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Task     string `json:"task"`
		Context  string `json:"context"`
		MaxTurns int    `json:"maxTurns"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid subagent arguments: %w", err))
	}
	if strings.TrimSpace(input.Task) == "" {
		return errorResult(fmt.Errorf("task is required"))
	}
	maxTurns := input.MaxTurns
	if maxTurns <= 0 {
		maxTurns = t.options.DefaultMaxTurns
	}
	if maxTurns > t.options.MaxTurns {
		maxTurns = t.options.MaxTurns
	}

	childTools := []agent.Tool{
		NewReadTool(t.cwd),
		NewListTool(t.cwd),
		NewGrepTool(t.cwd),
	}
	runner := agent.NewRunnerWithOptions(t.provider, childTools, agent.RunnerOptions{MaxTurns: maxTurns})
	messages := []agent.Message{
		{Role: agent.RoleSystem, Content: subagentSystemPrompt},
		{Role: agent.RoleUser, Content: formatSubagentTask(input.Task, input.Context)},
	}
	reply, err := runner.Run(ctx, messages, nil)
	if err != nil {
		return errorResult(err)
	}
	return textResult(reply.Content)
}

func formatSubagentTask(task, contextText string) string {
	if strings.TrimSpace(contextText) == "" {
		return task
	}
	return fmt.Sprintf("Task:\n%s\n\nContext:\n%s", task, contextText)
}

const subagentSystemPrompt = `You are a read-only subagent inside gg.
Focus on the assigned research task.
Use only read/list/grep tools.
Do not modify files or run shell commands.
Return a concise answer with relevant file paths and evidence.`
