package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Hit is a single keyword match inside the memory directory.
type Hit struct {
	Path    string // relative to the memory dir, e.g. "MEMORY.md" or "people/zhang-san.md"
	Line    int    // 1-based line number
	Snippet string // the matching line, trimmed
	Score   int    // number of query occurrences in the line
	mtime   int64  // tie-breaker: newer files first
}

// maxSearchHits caps the number of matches returned.
const maxSearchHits = 10

// Search scans the memory directory for query (case-insensitive substring).
// Scope is one of "all", "curated", "daily", "people", "groups". Results are
// ordered by match count, then by file recency.
func (s *Store) Search(query, scope string) ([]Hit, error) {
	hits, err := s.searchAll(query, scope)
	if err != nil {
		return nil, err
	}
	if len(hits) > maxSearchHits {
		hits = hits[:maxSearchHits]
	}
	return hits, nil
}

// searchAll is Search without the result cap, for layered merges that
// dedup across layers before applying their own cap. Capping each layer
// first would discard distinct matches that survive dedup.
func (s *Store) searchAll(query, scope string) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search query is required")
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "all", "curated", "daily", "people", "groups":
	default:
		return nil, fmt.Errorf("unknown search scope %q: want all|curated|daily|people|groups", scope)
	}
	lowered := strings.ToLower(query)
	var hits []Hit
	walkRoot := s.dir
	if s.strict {
		// Never walk a symlinked workspace layer: its target is not the
		// workspace's memory.
		realDir, err := resolveWorkspaceDir(s.dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		walkRoot = realDir
	}
	err := filepath.WalkDir(walkRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		if s.strict && entry.Type()&os.ModeSymlink != 0 {
			// A symlinked memory file could point outside the
			// workspace; skip it instead of following it into the
			// prompt.
			return nil
		}
		rel, err := filepath.Rel(walkRoot, path)
		if err != nil || !scopeMatch(scope, rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var mtime int64
		if info, err := entry.Info(); err == nil {
			mtime = info.ModTime().Unix()
		}
		for i, line := range strings.Split(string(data), "\n") {
			count := strings.Count(strings.ToLower(line), lowered)
			if count == 0 {
				continue
			}
			snippet := strings.TrimSpace(line)
			if len([]rune(snippet)) > 160 {
				snippet = string([]rune(snippet)[:157]) + "..."
			}
			hits = append(hits, Hit{
				Path:    filepath.ToSlash(rel),
				Line:    i + 1,
				Snippet: snippet,
				Score:   count,
				mtime:   mtime,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].mtime > hits[j].mtime
	})
	return hits, nil
}

func scopeMatch(scope, rel string) bool {
	rel = filepath.ToSlash(rel)
	switch scope {
	case "all":
		return true
	case "curated":
		return rel == "MEMORY.md"
	case "daily":
		if strings.Contains(rel, "/") {
			return false
		}
		_, err := time.Parse("2006-01-02", strings.TrimSuffix(rel, ".md"))
		return err == nil
	case "people":
		return strings.HasPrefix(rel, "people/")
	case "groups":
		return strings.HasPrefix(rel, "groups/")
	}
	return false
}
