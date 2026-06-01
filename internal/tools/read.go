package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
)

type ReadTool struct {
	cwd        string
	extraRoots []string
}

func NewReadTool(cwd string) ReadTool {
	return NewReadToolWithOptions(cwd, ReadOptions{})
}

type ReadOptions struct {
	ExtraRoots []string
}

func NewReadToolWithOptions(cwd string, options ReadOptions) ReadTool {
	return ReadTool{cwd: cwd, extraRoots: append([]string(nil), options.ExtraRoots...)}
}

func (t ReadTool) Name() string { return "read" }

func (t ReadTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "read",
		Description: "Read a text file within the current working directory or configured read-only skill directories. Supports optional 1-indexed offset and line limit.",
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
	path, err := t.resolve(input.Path)
	if err != nil {
		return errorResult(err)
	}
	data, truncated, err := readFilePrefix(path, defaultMaxReadBytes)
	if err != nil {
		return errorResult(err)
	}
	if containsNUL(data) {
		return errorResult(fmt.Errorf("binary file not readable as text: %s", input.Path))
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
	output := strings.Join(lines[start:end], "\n")
	if truncated {
		output += fmt.Sprintf("\n... truncated after %d bytes ...", defaultMaxReadBytes)
	}
	return textResult(output)
}

func (t ReadTool) resolve(path string) (string, error) {
	target, err := resolveExistingInsideRoot(t.cwd, path, "working directory")
	if err == nil {
		return target, nil
	}
	cwdErr := err
	if !filepath.IsAbs(path) {
		return "", cwdErr
	}
	for _, root := range t.extraRoots {
		target, err := resolveExistingInsideRoot(root, path, "configured read-only root")
		if err == nil {
			return target, nil
		}
	}
	return "", cwdErr
}

func readFilePrefix(path string, maxBytes int) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, truncated, nil
}
