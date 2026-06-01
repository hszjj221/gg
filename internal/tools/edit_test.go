package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditToolRequiresUniqueOldText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewEditTool(dir), `{"path":"main.go","edits":[{"oldText":"beta","newText":"gamma"}]}`)

	if !result.IsError {
		t.Fatalf("expected non-unique oldText to fail")
	}
	if !strings.Contains(result.Content[0].Text, "must match exactly once") {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
}

func TestEditToolRejectsSymlinkFileEscapeFromCWD(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "secret.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result := executeTool(t, NewEditTool(dir), `{"path":"secret.txt","edits":[{"oldText":"old","newText":"new"}]}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected result: %+v", result)
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old\n" {
		t.Fatalf("edit escaped cwd through symlink: %q", string(data))
	}
}

func TestEditToolAppliesMultipleReplacementsAgainstOriginalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc a() {}\nfunc b() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewEditTool(dir), `{"path":"main.go","edits":[{"oldText":"func a() {}","newText":"func alpha() {}"},{"oldText":"func b() {}","newText":"func beta() {}"}]}`)

	if result.IsError {
		t.Fatalf("expected edit success: %s", result.Content[0].Text)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); !strings.Contains(got, "func alpha() {}") || !strings.Contains(got, "func beta() {}") {
		t.Fatalf("replacements not applied:\n%s", got)
	}
}

func TestEditToolApprovalRequestPreviewsReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("before\nkeep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditTool(dir)

	req, err := tool.ApprovalRequest(json.RawMessage(`{"path":"file.txt","edits":[{"oldText":"before","newText":"after"}]}`))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"edit file.txt", "1 replacement", "before", "after"} {
		if !strings.Contains(req.Summary+"\n"+req.Details, want) {
			t.Fatalf("edit approval missing %q: %+v", want, req)
		}
	}
}

func TestEditToolApprovalRequestShowsChangeAfterLongCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	prefix := strings.Repeat("x", approvalPreviewLimit+100)
	if err := os.WriteFile(path, []byte(prefix+"\nold tail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditTool(dir)

	req, err := tool.ApprovalRequest(json.RawMessage(`{"path":"file.txt","edits":[{"oldText":"old tail","newText":"new tail"}]}`))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"old tail", "new tail"} {
		if !strings.Contains(req.Details, want) {
			t.Fatalf("approval preview should show changed tail %q:\n%s", want, req.Details)
		}
	}
}

func TestEditToolApprovalRequestReportsReplacementFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditTool(dir)

	_, err := tool.ApprovalRequest(json.RawMessage(`{"path":"file.txt","edits":[{"oldText":"missing","newText":"after"}]}`))
	if err == nil || !strings.Contains(err.Error(), `oldText "missing" must match exactly once`) {
		t.Fatalf("expected clear replacement error, got %v", err)
	}
}
