package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestWriteToolRejectsSymlinkParentEscapeFromCWD(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result := executeTool(t, NewWriteTool(dir), `{"path":"outside/file.txt","content":"secret"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("write escaped cwd through symlink")
	}
}

func TestWriteToolApprovalRequestPreviewsCreateAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(dir)

	create, err := tool.ApprovalRequest(json.RawMessage(`{"path":"new.txt","content":"new content\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if create.ToolName != "write" || !strings.Contains(create.Summary, "create new.txt") || !strings.Contains(create.Details, "new content") {
		t.Fatalf("unexpected create approval request: %+v", create)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("old content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overwrite, err := tool.ApprovalRequest(json.RawMessage(`{"path":"new.txt","content":"new content\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"overwrite new.txt", "old content", "new content"} {
		if !strings.Contains(overwrite.Summary+"\n"+overwrite.Details, want) {
			t.Fatalf("overwrite approval missing %q: %+v", want, overwrite)
		}
	}
}

func TestWriteToolApprovalRequestShowsOverwriteChangeAfterLongCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	prefix := strings.Repeat("x", approvalPreviewLimit+100)
	if err := os.WriteFile(path, []byte(prefix+"\nold tail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteTool(dir)

	req, err := tool.ApprovalRequest(json.RawMessage(`{"path":"file.txt","content":"` + prefix + `\nnew tail\n"}`))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"old tail", "new tail"} {
		if !strings.Contains(req.Details, want) {
			t.Fatalf("approval preview should show changed tail %q:\n%s", want, req.Details)
		}
	}
}
