package kb

import (
	"strings"
	"testing"
)

func TestChunkTextBasic(t *testing.T) {
	text := "Para one.\n\nPara two.\n\nPara three."
	chunks := ChunkText("doc.md", text, 100, 0)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Source != "doc.md" {
		t.Fatalf("want source doc.md, got %q", chunks[0].Source)
	}
}

func TestChunkTextSplitsLong(t *testing.T) {
	text := strings.Repeat("a", 500) + "\n\n" + strings.Repeat("b", 500)
	chunks := ChunkText("doc.md", text, 200, 0)
	if len(chunks) < 4 {
		t.Fatalf("want >=4 chunks for oversized paragraphs, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len(c.Text) > 200 {
			t.Fatalf("chunk exceeds maxChars: %d", len(c.Text))
		}
	}
}

func TestChunkTextOverlap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("paragraph number " + strings.Repeat("x", 40) + "\n\n")
	}
	chunks := ChunkText("doc.md", sb.String(), 200, 50)
	if len(chunks) < 2 {
		t.Fatalf("want multiple chunks, got %d", len(chunks))
	}
	// Consecutive chunks should share context.
	found := false
	for i := 1; i < len(chunks); i++ {
		if strings.Contains(chunks[i].Text, "paragraph number") && strings.Contains(chunks[i-1].Text, "paragraph number") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected overlapping content between consecutive chunks")
	}
}

func TestChunkTextKeepsCodeFences(t *testing.T) {
	text := "Intro.\n\n```go\nline1\n\nline2\n```\n\nOutro."
	chunks := ChunkText("doc.md", text, 10000, 0)
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "line1\n\nline2") {
		t.Fatal("code fence was split across chunks")
	}
}

func TestChunkTextEmpty(t *testing.T) {
	if got := ChunkText("doc.md", "   \n\n  ", 100, 0); len(got) != 0 {
		t.Fatalf("want no chunks for blank text, got %d", len(got))
	}
}

func TestChunkTextIDsUnique(t *testing.T) {
	text := strings.Repeat("para\n\n", 50)
	chunks := ChunkText("a/b.md", text, 60, 0)
	seen := map[string]bool{}
	for _, c := range chunks {
		if seen[c.ID] {
			t.Fatalf("duplicate chunk id %q", c.ID)
		}
		seen[c.ID] = true
	}
}
