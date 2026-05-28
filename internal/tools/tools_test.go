package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func executeTool(t *testing.T, tool Tool, input string) ToolResult {
	t.Helper()
	result := tool.Execute(context.Background(), json.RawMessage(input))
	if len(result.Content) == 0 {
		t.Fatalf("expected tool result content")
	}
	return result
}

func TestReadToolReadsRequestedLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewReadTool(dir), `{"path":"notes.txt","offset":2,"limit":2}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	if got, want := result.Content[0].Text, "two\nthree"; got != want {
		t.Fatalf("unexpected content:\nwant %q\ngot  %q", want, got)
	}
}

func TestReadToolRejectsPathOutsideCWD(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewReadTool(dir), `{"path":"`+filepath.ToSlash(outside)+`"}`)

	if !result.IsError {
		t.Fatalf("expected error for outside path")
	}
	if !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
}

func TestWriteToolCreatesParentDirectoriesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	result := executeTool(t, tool, `{"path":"nested/file.txt","content":"first"}`)
	if result.IsError {
		t.Fatalf("expected write success: %s", result.Content[0].Text)
	}
	result = executeTool(t, tool, `{"path":"nested/file.txt","content":"second"}`)
	if result.IsError {
		t.Fatalf("expected overwrite success: %s", result.Content[0].Text)
	}

	content, err := os.ReadFile(filepath.Join(dir, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "second" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

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
