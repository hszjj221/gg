package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBashToolReportsExitCodeAndOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, BashOptions{DefaultTimeout: 5 * time.Second})

	result := executeTool(t, tool, `{"command":"printf hello && exit 7","timeout":5}`)

	if !result.IsError {
		t.Fatalf("expected non-zero exit to be an error")
	}
	if !strings.Contains(result.Content[0].Text, "hello") || !strings.Contains(result.Content[0].Text, "exit code 7") {
		t.Fatalf("unexpected result: %s", result.Content[0].Text)
	}
}

func TestBashToolTruncatesLargeOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, BashOptions{DefaultTimeout: 5 * time.Second})

	result := executeTool(t, tool, `{"command":"yes x | head -c 307200","timeout":5}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	text := result.Content[0].Text
	if len(text) > 270*1024 {
		t.Fatalf("bash output was not capped, got %d bytes", len(text))
	}
	if !strings.Contains(text, "output truncated") {
		t.Fatalf("truncation marker missing from output")
	}
}

func TestBashToolApprovalRequestDescribesCommand(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, BashOptions{DefaultTimeout: 5 * time.Second})

	req, err := tool.ApprovalRequest(json.RawMessage(`{"command":"go test ./...","timeout":7}`))
	if err != nil {
		t.Fatal(err)
	}

	if req.ToolName != "bash" || !strings.Contains(req.Summary, "go test ./...") {
		t.Fatalf("unexpected bash approval summary: %+v", req)
	}
	for _, want := range []string{"command: go test ./...", "cwd: " + dir, "timeout: 7s"} {
		if !strings.Contains(req.Details, want) {
			t.Fatalf("bash approval details missing %q:\n%s", want, req.Details)
		}
	}
}
