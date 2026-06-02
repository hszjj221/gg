package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingMemoryFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gg", "memory.md")

	snapshot, err := Load(path, 100)
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.Exists || snapshot.Content != "" || snapshot.Tokens != 0 {
		t.Fatalf("unexpected missing snapshot: %+v", snapshot)
	}
}

func TestAppendCreatesMarkdownMemoryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gg", "memory.md")

	if err := Append(path, "Always answer in Simplified Chinese."); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, "Prefer tests before completion.\nKeep changes scoped."); err != nil {
		t.Fatal(err)
	}

	content, err := Show(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# gg Memory",
		"- Always answer in Simplified Chinese.",
		"- Prefer tests before completion.\n  Keep changes scoped.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("memory file missing %q:\n%s", want, content)
		}
	}
}

func TestAppendAddsNewlineAfterManualContentWithoutTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gg", "memory.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# gg Memory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Append(path, "Remember spacing."); err != nil {
		t.Fatal(err)
	}

	content, err := Show(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "# gg Memory\n- Remember spacing.") {
		t.Fatalf("append should separate manual content and bullet:\n%s", content)
	}
}

func TestShowMissingMemoryFileReturnsEmptyMessage(t *testing.T) {
	content, err := Show(filepath.Join(t.TempDir(), ".gg", "memory.md"))
	if err != nil {
		t.Fatal(err)
	}
	if content != emptyMessage {
		t.Fatalf("unexpected missing memory message: %q", content)
	}
}

func TestLoadTruncatesSnapshotWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gg", "memory.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "# gg Memory\n\n- " + strings.Repeat("remember ", 200)
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Load(path, 20)
	if err != nil {
		t.Fatal(err)
	}

	if !snapshot.Truncated || !strings.Contains(snapshot.Content, "memory truncated") {
		t.Fatalf("expected truncated snapshot: %+v", snapshot)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("memory file should not be modified")
	}
}

func TestSystemPromptIncludesMemoryContent(t *testing.T) {
	prompt := SystemPrompt(Snapshot{Content: "- Prefer small changes."})
	if !strings.Contains(prompt, "User memory from ~/.gg/memory.md:") || !strings.Contains(prompt, "Prefer small changes") {
		t.Fatalf("unexpected system prompt: %q", prompt)
	}
}
