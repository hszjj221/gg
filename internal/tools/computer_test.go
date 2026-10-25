package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

func TestParsePS(t *testing.T) {
	out := `    PID COMMAND         %CPU %MEM
      1 launchd            0.0  0.1
    123 myapp             12.5  2.3
  notanumber broken        x    y
`
	procs := parsePS(out)
	if len(procs) != 2 {
		t.Fatalf("expected 2 processes, got %d", len(procs))
	}
	if procs[0].PID != 1 || procs[0].Name != "launchd" || procs[0].CPU != 0.0 || procs[0].Mem != 0.1 {
		t.Errorf("unexpected first process: %+v", procs[0])
	}
	if procs[1].PID != 123 || procs[1].Name != "myapp" || procs[1].CPU != 12.5 || procs[1].Mem != 2.3 {
		t.Errorf("unexpected second process: %+v", procs[1])
	}
}

func TestParsePSCommandWithSpaces(t *testing.T) {
	// Linux allows spaces in the comm name (PR_SET_NAME); the parser must
	// read the numeric columns from the ends of the row instead of
	// requiring exactly four fields.
	out := `    PID COMMAND         %CPU %MEM
    456 hello world        99.0  1.2
`
	procs := parsePS(out)
	if len(procs) != 1 {
		t.Fatalf("expected 1 process, got %d", len(procs))
	}
	if procs[0].PID != 456 || procs[0].Name != "hello world" || procs[0].CPU != 99.0 || procs[0].Mem != 1.2 {
		t.Errorf("unexpected process: %+v", procs[0])
	}
}

func TestComputerToolDefinitions(t *testing.T) {
	tools := []agent.Tool{
		NewComputerInfoTool(),
		NewProcessListTool(),
		NewProcessKillTool(),
		NewOpenTool(""),
		NewNotifyTool(),
		NewClipboardReadTool(),
		NewClipboardWriteTool(),
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		def := tool.Definition()
		if def.Name != tool.Name() {
			t.Errorf("definition name %q != tool name %q", def.Name, tool.Name())
		}
		if seen[def.Name] {
			t.Errorf("duplicate tool name %q", def.Name)
		}
		seen[def.Name] = true
		if def.Description == "" {
			t.Errorf("tool %q has empty description", def.Name)
		}
	}
	for _, name := range []string{"computer_info", "process_list", "process_kill", "open", "notify", "clipboard_read", "clipboard_write"} {
		if !seen[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestComputerToolApprovalConsequences(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool agent.ApprovalDescriber
		raw  string
		r    *bool
		e    bool
		risk string
	}{
		{"process_kill term", NewProcessKillTool(), `{"pid":4242}`, new(false), false, agent.ApprovalRiskDangerous},
		{"process_kill kill", NewProcessKillTool(), `{"pid":4242,"signal":"kill"}`, new(false), false, agent.ApprovalRiskDangerous},
		{"open http", NewOpenTool("/workspace"), `{"target":"http://example.com"}`, new(false), true, ""},
		{"open https", NewOpenTool("/workspace"), `{"target":" https://example.com "}`, new(false), true, ""},
		{"open mixed-case scheme", NewOpenTool("/workspace"), `{"target":"HTTPS://example.com"}`, new(false), true, ""},
		{"open relative file", NewOpenTool("/workspace"), `{"target":"docs/a.md"}`, new(false), false, ""},
		{"open absolute file", NewOpenTool("/workspace"), `{"target":"/workspace/http://notes"}`, new(false), false, ""},
		{"open other scheme", NewOpenTool("/workspace"), `{"target":"mailto:a@b.c"}`, new(false), false, ""},
		{"clipboard_read", NewClipboardReadTool(), `{}`, nil, false, ""},
		{"clipboard_write", NewClipboardWriteTool(), `{"text":"hello"}`, nil, false, agent.ApprovalRiskConfirm},
		{"notify", NewNotifyTool(), `{"title":"Reminder","body":"Meeting soon"}`, nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := tc.tool.ApprovalRequest(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			assertToolConsequences(t, req, tc.r, new(tc.e), new(false), new(false))
			if req.Risk != tc.risk {
				t.Errorf("Risk = %q, want %q", req.Risk, tc.risk)
			}
			if tc.risk != "" || tc.e {
				for _, approver := range []agent.Approver{agent.DefaultApprover{}, agent.UnattendedApprover{AllowOrdinary: true}} {
					decision, err := approver.Approve(context.Background(), req)
					if err != nil || decision.Allow {
						t.Errorf("%T must deny without a human: %+v, %v", approver, decision, err)
					}
				}
			}
		})
	}
}

func TestParseProcessKillInput(t *testing.T) {
	self := os.Getpid()

	pid, force, err := parseProcessKillInput(json.RawMessage(`{"pid":1234}`))
	if err != nil || pid != 1234 || force {
		t.Errorf("term default: pid=%d force=%v err=%v", pid, force, err)
	}
	pid, force, err = parseProcessKillInput(json.RawMessage(`{"pid":1234,"signal":"kill"}`))
	if err != nil || pid != 1234 || !force {
		t.Errorf("kill signal: pid=%d force=%v err=%v", pid, force, err)
	}
	for _, raw := range []string{
		`{"pid":0}`, `{"pid":1}`, `{"pid":-5}`,
		`{"pid":` + strconv.Itoa(self) + `}`,
		`{"pid":1234,"signal":"hup"}`,
		`{"signal":"term"}`,
		`not json`,
	} {
		if _, _, err := parseProcessKillInput(json.RawMessage(raw)); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}

func TestProcessKillApprovalRequest(t *testing.T) {
	tool := NewProcessKillTool()
	req, err := tool.ApprovalRequest(json.RawMessage(`{"pid":4242,"signal":"kill"}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolName != "process_kill" {
		t.Errorf("ToolName = %q", req.ToolName)
	}
	if !strings.Contains(req.Summary, "4242") || !strings.Contains(req.Summary, "SIGKILL") {
		t.Errorf("summary should name pid and signal: %q", req.Summary)
	}
	if _, err := tool.ApprovalRequest(json.RawMessage(`{"pid":1}`)); err == nil {
		t.Error("expected approval request to reject pid 1")
	}
}

func TestOpenToolApprovalRequest(t *testing.T) {
	tool := NewOpenTool("")
	req, err := tool.ApprovalRequest(json.RawMessage(`{"target":"https://example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Summary, "https://example.com") {
		t.Errorf("summary should show the target: %q", req.Summary)
	}
	if _, err := tool.ApprovalRequest(json.RawMessage(`{"target":""}`)); err == nil {
		t.Error("expected error for empty target")
	}
	if _, err := tool.ApprovalRequest(json.RawMessage(`{"target":"` + strings.Repeat("x", 3000) + `"}`)); err == nil {
		t.Error("expected error for overlong target")
	}
}

func TestClipboardReadApprovalRequest(t *testing.T) {
	tool := NewClipboardReadTool()
	req, err := tool.ApprovalRequest(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolName != "clipboard_read" {
		t.Errorf("ToolName = %q", req.ToolName)
	}
	if !strings.Contains(strings.ToLower(req.Details), "sensitive") {
		t.Errorf("details should warn about sensitive data: %q", req.Details)
	}
}

func TestNotifyToolApprovalRequest(t *testing.T) {
	tool := NewNotifyTool()
	var _ agent.ApprovalDescriber = tool

	for _, tt := range []struct {
		name  string
		raw   string
		title string
		body  string
	}{
		{"title and body", `{"title":" Reminder ","body":" Meeting soon "}`, "Reminder", "Meeting soon"},
		{"title only", `{"title":"Reminder"}`, "Reminder", ""},
		{"body only", `{"body":"Meeting soon"}`, "", "Meeting soon"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := tool.ApprovalRequest(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if req.ToolName != "notify" || string(req.Arguments) != tt.raw {
				t.Errorf("unexpected tool name or arguments: %+v", req)
			}
			if !strings.Contains(req.Summary, tt.title) || !strings.Contains(req.Summary, tt.body) {
				t.Errorf("summary should preview title and body: %q", req.Summary)
			}
			if want := "title: " + tt.title + "\nbody: " + tt.body; req.Details != want {
				t.Errorf("details = %q, want %q", req.Details, want)
			}
			if req.ExternallyVisible == nil || !*req.ExternallyVisible || !req.RequiresHumanConfirm() {
				t.Error("OS notifications must assert E and require human confirmation")
			}
			assertToolConsequences(t, req, nil, new(true), new(false), new(false))
		})
	}
}

func TestNotifyToolApprovalPreviewTruncation(t *testing.T) {
	raw, err := json.Marshal(map[string]string{
		"title": strings.Repeat("提", 201),
		"body":  strings.Repeat("文", 2001),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := NewNotifyTool().ApprovalRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if want := `show OS notification "` + strings.Repeat("提", 80) + `…": "` + strings.Repeat("文", 120) + `…"`; req.Summary != want {
		t.Errorf("summary = %q, want %q", req.Summary, want)
	}
	if want := "title: " + strings.Repeat("提", 200) + "…\nbody: " + strings.Repeat("文", 2000) + "…"; req.Details != want {
		t.Errorf("details should match the truncated notification: %q", req.Details)
	}
}

func TestNotifyToolValidation(t *testing.T) {
	tool := NewNotifyTool()
	for _, raw := range []string{
		`not json`, `{"title":42}`, `{"body":false}`, `{}`, `null`,
		`{"title":"","body":""}`, `{"title":" \t ","body":" \n "}`,
	} {
		if _, err := tool.ApprovalRequest(json.RawMessage(raw)); err == nil {
			t.Errorf("expected approval request to reject %q", raw)
		}
		if res := tool.Execute(context.Background(), json.RawMessage(raw)); !res.IsError {
			t.Errorf("expected execution to reject %q", raw)
		}
	}
}

func TestClipboardWriteValidation(t *testing.T) {
	tool := NewClipboardWriteTool()
	res := tool.Execute(context.Background(), json.RawMessage(`{"text":"`+strings.Repeat("x", 100001)+`"}`))
	if !res.IsError {
		t.Error("expected error for overlong text")
	}
	res = tool.Execute(context.Background(), json.RawMessage(`not json`))
	if !res.IsError {
		t.Error("expected error for invalid JSON")
	}
}

func TestClipboardWriteApprovalRequest(t *testing.T) {
	// clipboard_write replaces shared system state, so it must pass through
	// the approval pipeline like the other write/side-effect tools.
	tool := NewClipboardWriteTool()
	var _ agent.ApprovalDescriber = tool
	req, err := tool.ApprovalRequest(json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolName != "clipboard_write" {
		t.Errorf("unexpected tool name: %q", req.ToolName)
	}
	if !strings.Contains(req.Details, "hello") {
		t.Errorf("approval details should preview the text: %q", req.Details)
	}
	if _, err := tool.ApprovalRequest(json.RawMessage(`not json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestOpenToolResolvesRelativeTarget(t *testing.T) {
	tool := NewOpenTool("/workspace")
	req, err := tool.ApprovalRequest(json.RawMessage(`{"target":"docs/a.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Summary, "/workspace/docs/a.md") {
		t.Errorf("relative target should resolve against the workspace: %q", req.Summary)
	}
	for _, target := range []string{"https://example.com/x", "/abs/path.md", "mailto:a@b.c"} {
		req, err := tool.ApprovalRequest(json.RawMessage(`{"target":"` + target + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(req.Summary, target) {
			t.Errorf("target %q should pass through untouched: %q", target, req.Summary)
		}
	}
	// Empty cwd keeps the old behavior: no resolution.
	plain := NewOpenTool("")
	req, err = plain.ApprovalRequest(json.RawMessage(`{"target":"docs/a.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Summary, "docs/a.md") || strings.Contains(req.Summary, "/workspace") {
		t.Errorf("empty cwd should not resolve: %q", req.Summary)
	}
}

func TestKillProcessSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("computer tools unsupported on Windows")
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("sleep not available: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if err := killProcess(cmd.Process.Pid, false); err != nil {
		t.Fatalf("killProcess(SIGTERM) failed: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil exit after SIGTERM")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit within 5s of SIGTERM")
	}
}

func TestComputerInfoExecute(t *testing.T) {
	tool := NewComputerInfoTool()
	res := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.IsError {
		t.Fatalf("computer_info failed: %v", res.Content)
	}
	text := res.Content[0].Text
	for _, want := range []string{"OS:", "Arch:", "CPU", "Memory:", "Disk"} {
		if !strings.Contains(text, want) {
			t.Errorf("computer_info output missing %q:\n%s", want, text)
		}
	}
}

func TestProcessListExecute(t *testing.T) {
	tool := NewProcessListTool()
	res := tool.Execute(context.Background(), json.RawMessage(`{"limit":5}`))
	if res.IsError {
		t.Fatalf("process_list failed: %v", res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "PID") || !strings.Contains(text, "COMMAND") {
		t.Errorf("process_list output missing header:\n%s", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least one process row, got:\n%s", text)
	}
	if len(lines) > 6 { // header + 5
		t.Errorf("limit=5 exceeded: %d lines", len(lines))
	}

	res = tool.Execute(context.Background(), json.RawMessage(`{"limit":3,"sort_by":"mem"}`))
	if res.IsError {
		t.Fatalf("process_list sort_by=mem failed: %v", res.Content)
	}
}
