package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListToolListsDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewListTool(dir), `{"path":"."}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "b.txt\nsubdir/" {
		t.Fatalf("unexpected list output: %q", got)
	}
}

func TestListToolRejectsOutsidePathAndFiles(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideResult := executeTool(t, NewListTool(dir), `{"path":"`+filepath.ToSlash(outside)+`"}`)
	if !outsideResult.IsError || !strings.Contains(outsideResult.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected outside result: %+v", outsideResult)
	}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileResult := executeTool(t, NewListTool(dir), `{"path":"file.txt"}`)
	if !fileResult.IsError || !strings.Contains(fileResult.Content[0].Text, "not a directory") {
		t.Fatalf("unexpected file result: %+v", fileResult)
	}
}

func TestListToolRejectsSymlinkDirectoryEscapeFromCWD(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result := executeTool(t, NewListTool(dir), `{"path":"outside"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected result: %+v", result)
	}
}
