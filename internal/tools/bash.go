package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

type BashOptions struct {
	DefaultTimeout time.Duration
}

type BashTool struct {
	cwd     string
	options BashOptions
}

func NewBashTool(cwd string, options BashOptions) BashTool {
	if options.DefaultTimeout == 0 {
		options.DefaultTimeout = 2 * time.Minute
	}
	return BashTool{cwd: cwd, options: options}
}

func (t BashTool) Name() string { return "bash" }

func (t BashTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "bash",
		Description: "Execute a shell command in the current working directory. Returns combined stdout and stderr.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
				"timeout": map[string]any{"type": "integer", "description": "timeout in seconds"},
			},
			"required": []string{"command"},
		},
	}
}

func (t BashTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid bash arguments: %w", err))
	}
	if input.Command == "" {
		return errorResult(fmt.Errorf("command is required"))
	}
	timeout := t.options.DefaultTimeout
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(runCtx, shell, "-lc", input.Command)
	cmd.Dir = t.cwd
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	text := output.String()
	if runCtx.Err() == context.DeadlineExceeded {
		return ToolResult{IsError: true, Content: []ContentBlock{{Type: ContentText, Text: text + fmt.Sprintf("\ncommand timed out after %s", timeout)}}}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ToolResult{IsError: true, Content: []ContentBlock{{Type: ContentText, Text: text + fmt.Sprintf("\nexit code %d", exitErr.ExitCode())}}}
		}
		return errorResult(err)
	}
	return textResult(text)
}
