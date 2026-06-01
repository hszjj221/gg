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

const maxBashOutputBytes = 256 * 1024

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

func (t BashTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid bash arguments: %w", err)
	}
	if input.Command == "" {
		return agent.ApprovalRequest{}, fmt.Errorf("command is required")
	}
	timeout := t.options.DefaultTimeout
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Second
	}
	return agent.ApprovalRequest{
		ToolName:  "bash",
		Summary:   "bash: " + input.Command,
		Details:   fmt.Sprintf("command: %s\ncwd: %s\ntimeout: %s", input.Command, t.cwd, timeout),
		Arguments: raw,
	}, nil
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
	output := newLimitedOutputBuffer(maxBashOutputBytes)
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

type limitedOutputBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated int
}

func newLimitedOutputBuffer(limit int) limitedOutputBuffer {
	return limitedOutputBuffer{limit: limit}
}

func (b *limitedOutputBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated += len(p)
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.truncated += len(p) - remaining
	}
	return len(p), nil
}

func (b *limitedOutputBuffer) String() string {
	text := b.buf.String()
	if b.truncated == 0 {
		return text
	}
	return text + fmt.Sprintf("\n... output truncated after %d bytes, omitted %d bytes ...", b.limit, b.truncated)
}
