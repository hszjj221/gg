package kb

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxFileBytes caps how much of a single file is read for indexing.
const maxFileBytes = 512 * 1024

// textExtensions is the allowlist of file types considered indexable text.
// Everything else is probed for binary content instead.
var textExtensions = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".rst": true,
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
	".java": true, ".c": true, ".cc": true, ".cpp": true, ".h": true,
	".rs": true, ".rb": true, ".php": true, ".sh": true, ".lua": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".xml": true, ".html": true, ".css": true, ".sql": true, ".proto": true,
}

// skipDirs are never descended into when building an index.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "__pycache__": true, ".venv": true, "target": true,
	"dist": true, "build": true, ".idea": true, ".vscode": true,
}

// BuildOptions controls index construction.
type BuildOptions struct {
	// MaxChars/Overlap tune chunking; zero values select the defaults.
	MaxChars int
	Overlap  int
	// Progress is called with (filesDone, chunksSoFar); may be nil.
	Progress func(filesDone, chunksSoFar int)
}

// BuildIndex walks root, chunks indexable text files, embeds the chunks,
// and returns a ready-to-save Index.
func BuildIndex(ctx context.Context, emb Embedder, name, root string, opts BuildOptions) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("kb: resolve root: %w", err)
	}
	files, err := collectTextFiles(abs)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("kb: no indexable text files under %s", abs)
	}
	var chunks []Chunk
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text, err := readTextFile(f)
		if err != nil {
			continue // unreadable file: skip, don't fail the whole build
		}
		rel, _ := filepath.Rel(abs, f)
		for _, c := range ChunkText(rel, text, opts.MaxChars, opts.Overlap) {
			chunks = append(chunks, c)
		}
		if opts.Progress != nil {
			opts.Progress(i+1, len(chunks))
		}
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("kb: files found but no chunks produced under %s", abs)
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	vectors, err := emb.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	return NewIndex(name, emb.Model(), chunks, vectors)
}

// collectTextFiles returns indexable text files under root, skipping
// version-control, dependency, and build directories.
func collectTextFiles(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable entry: skip
		}
		if info.IsDir() {
			if path != root && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isIndexable(path) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

func isIndexable(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if textExtensions[ext] {
		return true
	}
	// Extensionless or unknown: probe the head for binary content.
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 8000)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	for _, b := range head {
		if b == 0 {
			return false
		}
	}
	// Heuristic: mostly-printable probe passes as text.
	return true
}

func readTextFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
