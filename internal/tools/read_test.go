package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestReadToolAllowsExtraReadOnlyRoots(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "ca")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "guide.md"), []byte("use ca"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadToolWithOptions(dir, ReadOptions{ExtraRoots: []string{skillDir}})
	result := executeTool(t, tool, `{"path":"`+filepath.ToSlash(filepath.Join(skillDir, "references", "guide.md"))+`"}`)

	if result.IsError {
		t.Fatalf("expected extra root read success: %s", result.Content[0].Text)
	}
	if got := result.Content[0].Text; got != "use ca" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestReadToolRejectsSymlinkEscapeFromExtraReadOnlyRoot(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(t.TempDir(), "ca")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(skillDir, "secret.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	tool := NewReadToolWithOptions(dir, ReadOptions{ExtraRoots: []string{skillDir}})
	result := executeTool(t, tool, `{"path":"`+filepath.ToSlash(link)+`"}`)

	if !result.IsError {
		t.Fatalf("expected symlink escape to be rejected")
	}
	if !strings.Contains(result.Content[0].Text, "outside working directory") {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
}

func TestReadToolRejectsBinaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.bin"), []byte{'g', 'g', 0, 'b', 'i', 'n'}, 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewReadTool(dir), `{"path":"image.bin"}`)

	if !result.IsError || !strings.Contains(result.Content[0].Text, "binary file") {
		t.Fatalf("unexpected binary read result: %+v", result)
	}
}

func TestReadToolMarksLargeFileTruncation(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", defaultMaxReadBytes+1024)
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := executeTool(t, NewReadTool(dir), `{"path":"large.txt","limit":1}`)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	text := result.Content[0].Text
	if len(text) > defaultMaxReadBytes+512 {
		t.Fatalf("read output was not capped, got %d bytes", len(text))
	}
	if !strings.Contains(text, "truncated after") {
		t.Fatalf("truncation marker missing from read output")
	}
}
