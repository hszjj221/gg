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

type EditTool struct {
	cwd string
}

type editReplacement struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func NewEditTool(cwd string) EditTool {
	return EditTool{cwd: cwd}
}

func (t EditTool) Name() string { return "edit" }

func (t EditTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "edit",
		Description: "Edit a file with exact text replacements. Each oldText must match exactly once in the original file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
				"edits": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": map[string]any{"type": "string"},
							"newText": map[string]any{"type": "string"},
						},
						"required": []string{"oldText", "newText"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
	}
}

func (t EditTool) Execute(_ context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Path  string            `json:"path"`
		Edits []editReplacement `json:"edits"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid edit arguments: %w", err))
	}
	if len(input.Edits) == 0 {
		return errorResult(fmt.Errorf("edits must contain at least one replacement"))
	}
	path, err := resolveInsideCWD(t.cwd, input.Path)
	if err != nil {
		return errorResult(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errorResult(err)
	}
	original := string(data)
	edits, err := locateEdits(original, input.Edits)
	if err != nil {
		return errorResult(err)
	}
	updated := applyLocatedEdits(original, edits)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return errorResult(err)
	}
	return textResult(fmt.Sprintf("applied %d edit(s) to %s", len(edits), input.Path))
}

type locatedEdit struct {
	start int
	end   int
	new   string
	old   string
}

func locateEdits(original string, replacements []editReplacement) ([]locatedEdit, error) {
	edits := make([]locatedEdit, 0, len(replacements))
	for _, repl := range replacements {
		if repl.OldText == "" {
			return nil, fmt.Errorf("oldText must not be empty")
		}
		if count := strings.Count(original, repl.OldText); count != 1 {
			return nil, fmt.Errorf("oldText %q must match exactly once, matched %d times", repl.OldText, count)
		}
		start := strings.Index(original, repl.OldText)
		edits = append(edits, locatedEdit{
			start: start,
			end:   start + len(repl.OldText),
			old:   repl.OldText,
			new:   repl.NewText,
		})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end {
			return nil, fmt.Errorf("edits must not overlap")
		}
	}
	return edits, nil
}

func applyLocatedEdits(original string, edits []locatedEdit) string {
	var out strings.Builder
	cursor := 0
	for _, edit := range edits {
		out.WriteString(original[cursor:edit.start])
		out.WriteString(edit.new)
		cursor = edit.end
	}
	out.WriteString(original[cursor:])
	return out.String()
}
