package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func executeTool(t *testing.T, tool Tool, input string) ToolResult {
	t.Helper()
	result := tool.Execute(context.Background(), json.RawMessage(input))
	if len(result.Content) == 0 {
		t.Fatalf("expected tool result content")
	}
	return result
}
