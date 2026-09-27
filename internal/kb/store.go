package kb

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Index is the on-disk knowledge base: chunk texts plus their vectors.
// Search is brute-force cosine similarity, which is exact and dependency-free;
// it comfortably handles tens of thousands of chunks on a laptop. Past that,
// swap Search for an ANN index (HNSW) without changing the file format.
type Index struct {
	Name      string      `json:"name"`
	Model     string      `json:"model"`
	Dim       int         `json:"dim"`
	CreatedAt int64       `json:"createdAt"`
	Chunks    []Chunk     `json:"chunks"`
	Vectors   [][]float32 `json:"vectors"`
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

// Save persists the index atomically.
func (ix *Index) Save(kbDir string) error {
	if len(ix.Chunks) != len(ix.Vectors) {
		return fmt.Errorf("kb: chunks (%d) and vectors (%d) length mismatch", len(ix.Chunks), len(ix.Vectors))
	}
	dir := Dir(kbDir, ix.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("kb: create index dir: %w", err)
	}
	raw, err := json.Marshal(ix)
	if err != nil {
		return fmt.Errorf("kb: marshal index: %w", err)
	}
	tmp := filepath.Join(dir, "index.json.tmp")
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
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

// NewIndex assembles an index from chunks and their vectors.
func NewIndex(name, model string, chunks []Chunk, vectors [][]float32) (*Index, error) {
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
		Name:      name,
		Model:     model,
		Dim:       dim,
		CreatedAt: time.Now().Unix(),
		Chunks:    chunks,
		Vectors:   vectors,
	}, nil
}

// Search returns the topK chunks ranked by cosine similarity to queryVec.
func (ix *Index) Search(queryVec []float32, topK int) []Result {
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
	return results
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
