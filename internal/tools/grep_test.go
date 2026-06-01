package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepToolFindsMatchesWithPathAndLineNumber(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "a.txt"), []byte("alpha\nneedle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "b.txt"), []byte("needle again\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":"pkg","pattern":"needle","limit":1}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "pkg/a.txt:2: needle here" {
		t.Fatalf("unexpected grep output: %q", got)
	}
}

func TestGrepToolRejectsOutsidePath(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(outside, []byte("needle"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":"`+filepath.ToSlash(outside)+`","pattern":"needle"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGrepToolRejectsSymlinkFileEscapeFromCWD(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("needle"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "secret.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":"secret.txt","pattern":"needle"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGrepToolRejectsBinaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.bin"), []byte{'n', 'e', 'e', 'd', 'l', 'e', 0}, 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":"image.bin","pattern":"needle"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "binary file") {
		t.Fatalf("unexpected binary grep result: %+v", result)
	}
}

func TestGrepToolRejectsLargeFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("needle\n", 200000)), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":"large.txt","pattern":"needle"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "too large") {
		t.Fatalf("unexpected large grep result: %+v", result)
	}
}

func TestGrepToolSkipsBinaryAndLargeFilesInDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "binary.bin"), []byte{'n', 'e', 'e', 'd', 'l', 'e', 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("needle\n", 200000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "text.txt"), []byte("needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":".","pattern":"needle","limit":10}`)

	if result.IsError {
		t.Fatalf("expected directory grep success, got error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "text.txt:1: needle here" {
		t.Fatalf("unexpected directory grep output: %q", got)
	}
}

func TestGrepToolSkipsSymlinkEscapeDuringDirectoryWalk(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("needle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "secret.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result := executeTool(t, NewGrepTool(dir), `{"path":".","pattern":"needle"}`)

	if result.IsError {
		t.Fatalf("expected directory grep to skip symlink escape, got error: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "" {
		t.Fatalf("directory grep should skip symlink escape, got %q", got)
	}
}
