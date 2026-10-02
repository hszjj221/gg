package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Store owns the directory layout for structured memory:
//
//	dir/
//	  MEMORY.md        curated long-term memory (stable facts, preferences)
//	  2006-01-02.md    daily logs, one file per day
//	  people/<slug>.md per-person pages
//	  groups/<slug>.md per-group pages
type Store struct {
	dir string
	// strict rejects symlink escapes on every file access. It is set for
	// workspace layers (see NewWorkspaceStore), which can come from
	// untrusted checkouts; the global store keeps the historical
	// follow-symlinks behavior.
	strict bool
}

// NewStore returns a store rooted at dir. It performs no I/O; call
// EnsureLayout before first use.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// NewWorkspaceStore returns a store rooted at dir that refuses to follow
// symlinks escaping dir, for workspace memory layers that may live in
// untrusted checkouts. Reads and writes fail closed instead of following
// a planted symlink to external files.
func NewWorkspaceStore(dir string) *Store {
	return &Store{dir: dir, strict: true}
}

// resolve returns the real path to use for I/O on path. For ordinary
// stores it is the path unchanged; for strict workspace stores the path
// is resolved and contained (see resolveWorkspaceFile), so no symlink is
// left to be followed after the check.
func (s *Store) resolve(path string) (string, error) {
	if !s.strict {
		return path, nil
	}
	return resolveWorkspaceFile(s.dir, path)
}

// DefaultDir returns the conventional memory directory.
func DefaultDir(home string) string {
	return filepath.Join(home, ".gg", "memory")
}

// Dir returns the store root.
func (s *Store) Dir() string {
	return s.dir
}

// CuratedPath is the long-term memory file.
func (s *Store) CuratedPath() string {
	return filepath.Join(s.dir, "MEMORY.md")
}

// DailyPath returns the log file for the given day.
func (s *Store) DailyPath(t time.Time) string {
	return filepath.Join(s.dir, t.Format("2006-01-02")+".md")
}

// PersonPath returns the page for a person slug.
func (s *Store) PersonPath(slug string) string {
	return filepath.Join(s.dir, "people", sanitizeSlug(slug)+".md")
}

// GroupPath returns the page for a group slug.
func (s *Store) GroupPath(slug string) string {
	return filepath.Join(s.dir, "groups", sanitizeSlug(slug)+".md")
}

// EnsureLayout creates the directory tree and an empty curated file.
func (s *Store) EnsureLayout() error {
	for _, dir := range []string{s.dir, filepath.Join(s.dir, "people"), filepath.Join(s.dir, "groups")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	path := s.CuratedPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Create an empty file (no header): a header-only file would look
		// like real content to the prompt snapshot. Append writes its own
		// header when it creates a file.
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// MigrateFromFile moves a legacy ~/.gg/memory.md into the structured store.
// It returns a human-readable notice ("" when nothing happened) and never
// merges: when the curated file already has real content the legacy file is
// retired to .bak untouched so the user can merge it by hand.
func (s *Store) MigrateFromFile(oldPath string) (string, error) {
	data, err := os.ReadFile(oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	// retire moves the legacy file aside. A concurrent gg process may have
	// retired it first; that just means the migration already happened.
	retire := func() error {
		if err := os.Rename(oldPath, oldPath+".bak"); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if strings.TrimSpace(string(data)) == "" {
		// Empty legacy file: nothing to migrate, retire quietly.
		if err := retire(); err != nil {
			return "", err
		}
		return "", nil
	}
	if err := s.EnsureLayout(); err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(s.CuratedPath()); err == nil && strings.TrimSpace(string(existing)) != "" {
		if err := retire(); err != nil {
			return "", err
		}
		return fmt.Sprintf("legacy %s not merged: %s already has content (legacy kept as %s.bak, merge by hand)",
			oldPath, s.CuratedPath(), oldPath), nil
	}
	if err := os.WriteFile(s.CuratedPath(), data, 0o600); err != nil {
		return "", err
	}
	if err := retire(); err != nil {
		return "", err
	}
	return fmt.Sprintf("migrated %s to %s (legacy file kept as %s.bak)",
		oldPath, s.CuratedPath(), oldPath), nil
}

// AppendCurated appends to MEMORY.md (the pre-structured Append behavior).
func (s *Store) AppendCurated(text string) error {
	path, err := s.resolve(s.CuratedPath())
	if err != nil {
		return err
	}
	return Append(path, text)
}

// AppendDaily appends a timestamped entry to today's log.
func (s *Store) AppendDaily(text string) error {
	now := time.Now()
	path, err := s.resolve(s.DailyPath(now))
	if err != nil {
		return err
	}
	return s.appendDated(path, now.Format("15:04"), text)
}

func (s *Store) appendDated(path, stamp, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("memory text is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "- %s %s\n", stamp, formatBulletText(text))
	return err
}

// AppendPerson appends to a person's page, creating it with a title header
// on first use.
func (s *Store) AppendPerson(slug, text string) error {
	if strings.TrimSpace(slug) == "" {
		return fmt.Errorf("person name is required")
	}
	path, err := s.resolve(s.PersonPath(slug))
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("# "+strings.TrimSpace(slug)+"\n"), 0o600); err != nil {
			return err
		}
	}
	return Append(path, text)
}

// AppendGroup appends to a group's page, creating it with a title header
// on first use.
func (s *Store) AppendGroup(slug, text string) error {
	if strings.TrimSpace(slug) == "" {
		return fmt.Errorf("group name is required")
	}
	path, err := s.resolve(s.GroupPath(slug))
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("# "+strings.TrimSpace(slug)+"\n"), 0o600); err != nil {
			return err
		}
	}
	return Append(path, text)
}

// PruneDaily deletes daily logs older than retentionDays and returns the
// deletion count. retentionDays <= 0 keeps everything; MEMORY.md and the
// people/groups pages are never pruned.
func (s *Store) PruneDaily(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	dir := s.dir
	if s.strict {
		// A symlinked workspace layer must not have its target's files
		// pruned; resolve (and validate) first.
		realDir, err := resolveWorkspaceDir(s.dir)
		if err != nil {
			if os.IsNotExist(err) {
				return 0, nil
			}
			return 0, err
		}
		dir = realDir
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Format("2006-01-02")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pruned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) != len("2006-01-02.md") || !strings.HasSuffix(name, ".md") {
			continue
		}
		day := strings.TrimSuffix(name, ".md")
		if _, err := time.Parse("2006-01-02", day); err != nil {
			continue
		}
		if day < cutoff {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return pruned, err
			}
			pruned++
		}
	}
	return pruned, nil
}

// LoadCurated loads MEMORY.md within the token budget.
func (s *Store) LoadCurated(maxPromptTokens int) (Snapshot, error) {
	return Load(s.CuratedPath(), maxPromptTokens)
}

// LoadDailyTail loads the tail of today's log within the token budget,
// keeping the most recent lines.
func (s *Store) LoadDailyTail(maxPromptTokens int) (Snapshot, error) {
	return loadTail(s.DailyPath(time.Now()), maxPromptTokens)
}

func loadTail(path string, maxPromptTokens int) (Snapshot, error) {
	snapshot := Snapshot{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, nil
		}
		return Snapshot{}, err
	}
	snapshot.Exists = true
	content := strings.TrimSpace(string(data))
	if content == "" || maxPromptTokens <= 0 {
		return snapshot, nil
	}
	lines := strings.Split(content, "\n")
	maxUnits := maxPromptTokens * 4
	used := 0
	start := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		next := tokenUnits(lines[i]) + 1 // +1 for the newline
		if used+next > maxUnits {
			break
		}
		used += next
		start = i
	}
	snapshot.Content = strings.Join(lines[start:], "\n")
	snapshot.Tokens = EstimateText(content)
	snapshot.Truncated = start > 0
	return snapshot, nil
}

// sanitizeSlug maps a person/group name to a safe filename. Unicode letters
// are kept so CJK names work; anything else becomes a dash or is dropped,
// which also neutralizes path traversal.
func sanitizeSlug(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	var b strings.Builder
	for _, r := range slug {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	return out
}
