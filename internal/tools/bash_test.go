package tools

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestBashToolReportsExitCodeAndOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, BashOptions{DefaultTimeout: 5 * time.Second})

	result := executeBashCommand(t, tool, platformCommand("printf hello && exit 7", "echo hello & exit /b 7"), 5)

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

	result := executeBashCommand(t, tool, platformCommand(
		"yes x | head -c 307200",
		powershellCommand(`[Console]::Out.Write(('x' * 307200))`),
	), 5)

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

func executeBashCommand(t *testing.T, tool BashTool, command string, timeout int) ToolResult {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"command": command, "timeout": timeout})
	if err != nil {
		t.Fatal(err)
	}
	return executeTool(t, tool, string(raw))
}

func platformCommand(unix, windows string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return unix
}

func powershellCommand(script string) string {
	codeUnits := utf16.Encode([]rune(script))
	data := make([]byte, len(codeUnits)*2)
	for index, codeUnit := range codeUnits {
		binary.LittleEndian.PutUint16(data[index*2:], codeUnit)
	}
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(data)
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
