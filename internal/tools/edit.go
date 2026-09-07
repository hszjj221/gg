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

func (t EditTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input struct {
		Path  string            `json:"path"`
		Edits []editReplacement `json:"edits"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid edit arguments: %w", err)
	}
	if len(input.Edits) == 0 {
		return agent.ApprovalRequest{}, fmt.Errorf("edits must contain at least one replacement")
	}
	path, err := resolveExistingInsideCWD(t.cwd, input.Path)
	if err != nil {
		return agent.ApprovalRequest{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.ApprovalRequest{}, err
	}
	original := string(data)
	edits, err := locateEdits(original, input.Edits)
	if err != nil {
		return agent.ApprovalRequest{}, err
	}
	updated := applyLocatedEdits(original, edits)
	replacementLabel := "replacements"
	if len(edits) == 1 {
		replacementLabel = "replacement"
	}
	return agent.ApprovalRequest{
		ToolName:  "edit",
		Summary:   fmt.Sprintf("edit %s (%d %s)", input.Path, len(edits), replacementLabel),
		Details:   fmt.Sprintf("path: %s\nreplacements: %d\n\n%s", input.Path, len(edits), contentChangePreview(original, updated)),
		Arguments: raw,
	}, nil
}

func (t EditTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	if err := ctx.Err(); err != nil {
		return errorResult(err)
	}
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
	path, err := resolveExistingInsideCWD(t.cwd, input.Path)
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
	if err := writeFileAtomic(ctx, path, []byte(updated)); err != nil {
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
	normalized := normalizeNewlines(original)
	for _, repl := range replacements {
		repl.OldText = normalizeNewlines(repl.OldText)
		if repl.OldText == "" {
			return nil, fmt.Errorf("oldText must not be empty")
		}
		if count := strings.Count(normalized, repl.OldText); count != 1 {
			return nil, fmt.Errorf("oldText %q must match exactly once, matched %d times", repl.OldText, count)
		}
		index := strings.Index(normalized, repl.OldText)
		start := originalOffset(original, index)
		end := originalOffset(original, index+len(repl.OldText))
		newText := normalizeNewlines(repl.NewText)
		if strings.Contains(original[start:end], "\r\n") || (strings.Contains(original, "\r\n") && !strings.Contains(strings.ReplaceAll(original, "\r\n", ""), "\n")) {
			newText = strings.ReplaceAll(newText, "\n", "\r\n")
		}
		edits = append(edits, locatedEdit{
			start: start,
			end:   end,
			old:   repl.OldText,
			new:   newText,
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

func originalOffset(text string, normalizedOffset int) int {
	i := 0
	for n := 0; n < normalizedOffset; n++ {
		if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			i++
		}
		i++
	}
	return i
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
