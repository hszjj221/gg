package mcp

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Function-calling name constraints (OpenAI-compatible providers):
// letters, digits, underscore, hyphen, max 64 characters.
const maxToolNameLength = 64

// descriptionTruncateRunes caps descriptions surfaced to the model.
const descriptionTruncateRunes = 1024

// SanitizeToolName maps an MCP server/tool pair to a function-calling-safe
// tool name. The mapping is one-way; the original names are preserved on
// the adapted tool for the actual tools/call.
func SanitizeToolName(server, tool string) string {
	s := sanitizePart(server)
	if s == "" {
		s = "server"
	}
	t := sanitizePart(tool)
	if t == "" {
		t = "tool"
	}
	name := "mcp_" + s + "_" + t
	if len(name) > maxToolNameLength {
		name = name[:maxToolNameLength]
	}
	return name
}

// sanitizePart keeps [a-zA-Z0-9_-] (what function-calling names allow),
// replaces the rest with '_', and trims edge underscores. The output is
// pure ASCII, so byte-wise truncation in SanitizeToolName is safe.
func sanitizePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// NormalizeInputSchema converts an MCP tool's inputSchema (any JSON value)
// into a plain JSON Schema object map suitable for OpenAI-style function
// calling. gg's providers don't use strict mode, so the normalization is
// light: guarantee the top-level object shape and cap description lengths.
// Unknown keywords (oneOf, $ref, ...) pass through untouched — providers
// ignore keywords they don't understand.
func NormalizeInputSchema(schema any) map[string]any {
	out := map[string]any{"type": "object"}
	if schema == nil {
		out["properties"] = map[string]any{}
		return out
	}
	data, err := json.Marshal(schema)
	if err != nil {
		out["properties"] = map[string]any{}
		return out
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		out["properties"] = map[string]any{}
		return out
	}
	for k, v := range decoded {
		out[k] = v
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if _, ok := out["properties"].(map[string]any); !ok {
		out["properties"] = map[string]any{}
	}
	truncateDescriptions(out)
	return out
}

// truncateDescriptions caps every "description" string in the schema tree
// so a single verbose server cannot blow up the model's tool definitions.
func truncateDescriptions(node any) {
	obj, ok := node.(map[string]any)
	if !ok {
		return
	}
	for k, v := range obj {
		if k == "description" {
			if s, ok := v.(string); ok {
				obj[k] = truncateRunes(s, descriptionTruncateRunes)
			}
			continue
		}
		switch child := v.(type) {
		case map[string]any:
			truncateDescriptions(child)
		case []any:
			for _, item := range child {
				truncateDescriptions(item)
			}
		}
	}
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit])
}
