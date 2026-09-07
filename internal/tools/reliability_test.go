package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadOffsetBeyondPrefixLimit(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line-%04d %s\n", i, strings.Repeat("x", 90))
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	result := NewReadTool(dir).Execute(context.Background(), []byte(`{"path":"big.txt","offset":2700,"limit":1}`))
	if result.IsError || !strings.HasPrefix(result.Content[0].Text, "line-2700") {
		t.Fatalf("line 2700 exists but read returned %+v", result)
	}
}
func TestReadThenEditCRLFFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("first\r\nsecond\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read := NewReadTool(dir).Execute(context.Background(), []byte(`{"path":"a.txt"}`))
	raw, _ := json.Marshal(map[string]any{"path": "a.txt", "edits": []map[string]string{{"oldText": read.Content[0].Text, "newText": "updated"}}})
	result := NewEditTool(dir).Execute(context.Background(), raw)
	if result.IsError {
		t.Fatalf("editing exact read result failed: %s", result.Content[0].Text)
	}
}
func TestBashTimeoutStopsChildPromptly(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	start := time.Now()
	result := NewBashTool(t.TempDir(), BashOptions{DefaultTimeout: 50 * time.Millisecond}).Execute(context.Background(), []byte(`{"command":"sleep 1 & wait"}`))
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("50ms timeout took %s (child held output pipe); result=%+v", elapsed, result)
	}
}

func TestBashKeepsFailureTailAndReadableFullLog(t *testing.T) {
	dir := t.TempDir()
	result := NewBashTool(dir, BashOptions{}).Execute(context.Background(), []byte(`{"command":"printf START; yes x | head -c 60000; printf FAILURE_AT_END; exit 2"}`))
	if !result.IsError || !strings.Contains(result.Content[0].Text, "FAILURE_AT_END") {
		t.Fatalf("missing failure tail: %+v", result)
	}
	_, path, ok := strings.Cut(result.Content[0].Text, "Full output saved to: ")
	if !ok {
		t.Fatal("missing full log path")
	}
	path = strings.SplitN(path, "\n", 2)[0]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "START") || !strings.HasSuffix(string(data), "FAILURE_AT_END") {
		t.Fatal("full log was truncated")
	}
	raw, _ := json.Marshal(map[string]any{"path": path, "offset": 30001, "limit": 2})
	read := NewReadTool(dir).Execute(context.Background(), raw)
	if read.IsError || !strings.Contains(read.Content[0].Text, "FAILURE_AT_END") {
		t.Fatalf("saved log cannot be paged: %+v", read)
	}
}

func TestAtomicEditPreservesCRLFAndFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("first\r\nsecond\r\nuntouched\r\n"), 0755); err != nil {
		t.Fatal(err)
	}
	result := NewEditTool(dir).Execute(context.Background(), []byte(`{"path":"script.sh","edits":[{"oldText":"first\nsecond","newText":"new\nlines"}]}`))
	if result.IsError {
		t.Fatalf("edit failed: %+v", result)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\r\nlines\r\nuntouched\r\n" || info.Mode().Perm() != 0755 {
		t.Fatalf("format or mode changed: %q mode=%v", data, info.Mode())
	}
}

func TestCanceledWriteDoesNotTouchFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := NewWriteTool(dir).Execute(ctx, []byte(`{"path":"keep.txt","content":"changed"}`))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || string(data) != "original" {
		t.Fatal("canceled write changed the file")
	}
}
