package kb

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Index is the on-disk knowledge base: chunk texts plus their vectors.
// Search is brute-force cosine similarity, which is exact and dependency-free;
// it comfortably handles tens of thousands of chunks on a laptop. Past that,
// swap Search for an ANN index (HNSW) without changing the file format.
type Index struct {
	Name  string `json:"name"`
	Model string `json:"model"`
	Dim   int    `json:"dim"`
	// EmbedBaseURL records the embeddings endpoint the index was built with
	// (normalized, no trailing slash). Empty means "the chat provider's
	// endpoint" (indexes built before this field existed).
	EmbedBaseURL string      `json:"embedBaseURL,omitempty"`
	CreatedAt    int64       `json:"createdAt"`
	Chunks       []Chunk     `json:"chunks"`
	Vectors      [][]float32 `json:"vectors"`
}

// Result is a single ranked hit.
type Result struct {
	Chunk Chunk
	Score float64 // cosine similarity in [-1, 1]
}

// DefaultName is used when the user does not pick a knowledge base name.
const DefaultName = "default"

// Dir returns the directory holding the named knowledge base.
func Dir(kbDir, name string) string {
	if name == "" {
		name = DefaultName
	}
	return filepath.Join(kbDir, name)
}

// IndexPath returns the index file path for the named knowledge base.
func IndexPath(kbDir, name string) string {
	return filepath.Join(Dir(kbDir, name), "index.json")
}

// Exists reports whether the named knowledge base has a built index.
func Exists(kbDir, name string) bool {
	_, err := os.Stat(IndexPath(kbDir, name))
	return err == nil
}

// Save persists the index atomically. The index directory and file are
// created user-private (0700/0600): the file contains the full text of every
// indexed document, not just embeddings, so it must not be world-readable.
func (ix *Index) Save(kbDir string) error {
	if len(ix.Chunks) != len(ix.Vectors) {
		return fmt.Errorf("kb: chunks (%d) and vectors (%d) length mismatch", len(ix.Chunks), len(ix.Vectors))
	}
	dir := Dir(kbDir, ix.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("kb: create index dir: %w", err)
	}
	raw, err := json.Marshal(ix)
	if err != nil {
		return fmt.Errorf("kb: marshal index: %w", err)
	}
	tmp := filepath.Join(dir, "index.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("kb: write index: %w", err)
	}
	if err := os.Rename(tmp, IndexPath(kbDir, ix.Name)); err != nil {
		return fmt.Errorf("kb: publish index: %w", err)
	}
	return nil
}

// Load reads a built index.
func Load(kbDir, name string) (*Index, error) {
	raw, err := os.ReadFile(IndexPath(kbDir, name))
	if err != nil {
		return nil, fmt.Errorf("kb: read index: %w", err)
	}
	var ix Index
	if err := json.Unmarshal(raw, &ix); err != nil {
		return nil, fmt.Errorf("kb: decode index: %w", err)
	}
	if len(ix.Chunks) != len(ix.Vectors) {
		return nil, fmt.Errorf("kb: corrupt index: chunks (%d) and vectors (%d) length mismatch",
			len(ix.Chunks), len(ix.Vectors))
	}
	return &ix, nil
}

// NewIndex assembles an index from chunks and their vectors. embedBaseURL
// records the embeddings endpoint used (normalized, no trailing slash);
// empty means the chat provider's endpoint.
func NewIndex(name, model, embedBaseURL string, chunks []Chunk, vectors [][]float32) (*Index, error) {
	if len(chunks) != len(vectors) {
		return nil, fmt.Errorf("kb: chunks (%d) and vectors (%d) length mismatch", len(chunks), len(vectors))
	}
	dim := 0
	if len(vectors) > 0 {
		dim = len(vectors[0])
	}
	for i, v := range vectors {
		if len(v) != dim {
			return nil, fmt.Errorf("kb: vector %d has dim %d, want %d", i, len(v), dim)
		}
	}
	return &Index{
		Name:         name,
		Model:        model,
		Dim:          dim,
		EmbedBaseURL: strings.TrimRight(embedBaseURL, "/"),
		CreatedAt:    time.Now().Unix(),
		Chunks:       chunks,
		Vectors:      vectors,
	}, nil
}

// Search returns the topK chunks ranked by cosine similarity to queryVec.
// It rejects query vectors whose dimension does not match the index:
// truncating or padding silently would return plausible-looking but corrupt
// rankings instead of exposing the incompatible configuration.
func (ix *Index) Search(queryVec []float32, topK int) ([]Result, error) {
	if ix.Dim <= 0 {
		return nil, fmt.Errorf("kb: index %q has no usable dimension", ix.Name)
	}
	if len(queryVec) != ix.Dim {
		return nil, fmt.Errorf("kb: query vector dim %d does not match index dim %d (model %s); the query was embedded with a different model or endpoint",
			len(queryVec), ix.Dim, ix.Model)
	}
	if topK <= 0 {
		topK = 5
	}
	results := make([]Result, 0, len(ix.Chunks))
	for i, c := range ix.Chunks {
		results = append(results, Result{Chunk: c, Score: cosine(queryVec, ix.Vectors[i])})
	}
	sort.Slice(results, func(a, b int) bool { return results[a].Score > results[b].Score })
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
