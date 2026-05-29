package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
)

const (
	defaultGrepLimit = 50
	maxGrepLimit     = 200
)

type GrepTool struct {
	cwd string
}

func NewGrepTool(cwd string) GrepTool {
	return GrepTool{cwd: cwd}
}

func (t GrepTool) Name() string { return "grep" }

func (t GrepTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "grep",
		Description: "Search text files under the current working directory using substring matching. Returns path:line: text.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "file or directory path, defaults to ."},
				"pattern": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t GrepTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid grep arguments: %w", err))
	}
	if input.Pattern == "" {
		return errorResult(fmt.Errorf("pattern is required"))
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = "."
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultGrepLimit
	}
	if limit > maxGrepLimit {
		limit = maxGrepLimit
	}
	path, err := resolveInsideCWD(t.cwd, input.Path)
	if err != nil {
		return errorResult(err)
	}

	var matches []string
	info, err := os.Stat(path)
	if err != nil {
		return errorResult(err)
	}
	if !info.IsDir() {
		if err := grepFile(t.cwd, path, input.Pattern, limit, &matches); err != nil {
			return errorResult(err)
		}
		return textResult(strings.Join(matches, "\n"))
	}
	err = filepath.WalkDir(path, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		return grepFile(t.cwd, filePath, input.Pattern, limit, &matches)
	})
	if err != nil {
		return errorResult(err)
	}
	return textResult(strings.Join(matches, "\n"))
}

func grepFile(cwd, path, pattern string, limit int, matches *[]string) error {
	if len(*matches) >= limit {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	for i, line := range lines {
		if len(*matches) >= limit {
			return nil
		}
		if strings.Contains(line, pattern) {
			*matches = append(*matches, fmt.Sprintf("%s:%d: %s", rel, i+1, line))
		}
	}
	return nil
}
