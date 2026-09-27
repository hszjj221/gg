package kb

import (
	"strings"
	"unicode/utf8"
)

// Chunk is a single retrievable unit of text.
type Chunk struct {
	ID     string `json:"id"`
	Source string `json:"source"` // path relative to the indexed root
	Text   string `json:"text"`
}

// Chunking defaults. Chunks target ~1200 characters with a 150-character
// overlap; small enough to keep retrieval precise, large enough to carry
// context. Tune via ChunkText options for other corpora.
const (
	DefaultMaxChars = 1200
	DefaultOverlap  = 150
)

// ChunkText splits text into chunks for embedding. It prefers paragraph
// boundaries (blank lines), merges short paragraphs up to maxChars, and
// hard-splits oversized paragraphs. Consecutive chunks share an overlap
// window so a concept split across a boundary is still retrievable.
func ChunkText(source, text string, maxChars, overlap int) []Chunk {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 4
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	paragraphs := splitParagraphs(text)
	var chunks []Chunk
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			chunks = append(chunks, Chunk{Source: source, Text: s})
		}
		cur.Reset()
	}
	for _, p := range paragraphs {
		for len(p) > maxChars {
			// Oversized paragraph: flush what we have, then hard-split.
			flush()
			cut := maxChars
			for cut > 0 && !utf8.ValidString(p[:cut]) {
				cut--
			}
			if cut == 0 {
				// No nonempty valid-UTF-8 prefix (e.g. the paragraph starts
				// with invalid bytes): consume one byte so the loop always
				// makes progress instead of spinning forever.
				cut = 1
			}
			chunks = append(chunks, Chunk{Source: source, Text: strings.TrimSpace(p[:cut])})
			p = strings.TrimSpace(p[cut:])
		}
		if cur.Len()+len(p)+1 > maxChars && cur.Len() > 0 {
			flush()
			if tail := chunkTail(chunks, overlap); tail != "" {
				cur.WriteString(tail)
				cur.WriteString("\n\n")
			}
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	flush()
	for i := range chunks {
		chunks[i].ID = chunkID(source, i)
	}
	return chunks
}

// splitParagraphs splits on blank lines, keeping fenced code blocks intact.
func splitParagraphs(text string) []string {
	var out []string
	var cur strings.Builder
	inFence := false
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if trimmed == "" && !inFence {
			flush()
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	flush()
	return out
}

// chunkTail returns the last overlap runes of the most recent chunk so the
// next chunk can share context across the boundary.
func chunkTail(chunks []Chunk, overlap int) string {
	if len(chunks) == 0 || overlap == 0 {
		return ""
	}
	runes := []rune(chunks[len(chunks)-1].Text)
	if len(runes) <= overlap {
		return string(runes)
	}
	return string(runes[len(runes)-overlap:])
}

func chunkID(source string, i int) string {
	return strings.ReplaceAll(source, "/", "_") + "#" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [16]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
