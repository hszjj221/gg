// Package library manages the user's file collection at ~/.gg/library/:
// files the user uploads plus agent-generated files saved on artifact
// publish.
package library

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/filelock"
)

// ErrNotFound is returned when a library entry does not exist.
var ErrNotFound = errors.New("library entry not found")

// maxUploadBytes caps a single library file; the library is a curated
// collection, not a backup target.
const maxUploadBytes = 50 << 20 // 50 MiB

// Source marks where an entry came from.
const (
	SourceUpload = "upload"
)

// Entry is one file in the library.
type Entry struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Source  string    `json:"source"` // "upload" or "artifact:<id>"
	Size    int64     `json:"size"`
	AddedAt time.Time `json:"added_at"`
}

// Store keeps files plus an index.json under dir.
type Store struct {
	dir string
}

// Open creates dir (0700) when missing and tightens permissions on an
// existing one.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create library dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700) // best-effort
	s := &Store{dir: dir}
	if _, err := os.Stat(s.indexPath()); os.IsNotExist(err) {
		if err := s.writeIndex(nil); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Dir returns the store root.
func (s *Store) Dir() string { return s.dir }

func (s *Store) indexPath() string { return filepath.Join(s.dir, "index.json") }

func (s *Store) lockPath() string { return filepath.Join(s.dir, "library.lock") }

// indexFileName is the store's own metadata file; it must never be taken as
// a library entry name, or an upload would overwrite the index and a later
// remove would delete it.
const indexFileName = "index.json"

// withLock serializes read-modify-write cycles across processes so two
// concurrent adds cannot reserve the same name or drop each other's index
// entries.
func (s *Store) withLock(fn func() error) error {
	unlock, err := filelock.Lock(s.lockPath())
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func newID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate library id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Add copies srcPath into the library. name defaults to the source file's
// base name; collisions get a numeric suffix. source is SourceUpload or
// "artifact:<id>".
func (s *Store) Add(srcPath, name, source string) (*Entry, error) {
	var out *Entry
	err := s.withLock(func() error {
		fi, err := os.Stat(srcPath)
		if err != nil {
			return fmt.Errorf("stat source file: %w", err)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", srcPath)
		}
		if fi.Size() > maxUploadBytes {
			return fmt.Errorf("file exceeds %d bytes", maxUploadBytes)
		}
		if name == "" {
			name = filepath.Base(srcPath)
		}
		entries, err := s.readIndex()
		if err != nil {
			return err
		}
		name, err = s.reserveName(entries, name)
		if err != nil {
			return err
		}
		dst := filepath.Join(s.dir, name)
		size, err := copyFileAtomic(srcPath, dst)
		if err != nil {
			return err
		}
		out, err = s.record(entries, name, source, size)
		return err
	})
	return out, err
}

// AddBytes stores in-memory content under name (e.g. an artifact's published
// version) without a source file on disk.
func (s *Store) AddBytes(name string, content []byte, source string) (*Entry, error) {
	var out *Entry
	err := s.withLock(func() error {
		if len(content) > maxUploadBytes {
			return fmt.Errorf("content exceeds %d bytes", maxUploadBytes)
		}
		entries, err := s.readIndex()
		if err != nil {
			return err
		}
		name, err = s.reserveName(entries, name)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(s.dir, name), content, 0o600); err != nil {
			return err
		}
		out, err = s.record(entries, name, source, int64(len(content)))
		return err
	})
	return out, err
}

// reserveName sanitizes name (falling back to the source base name) and
// dedupes collisions.
func (s *Store) reserveName(entries []*Entry, name string) (string, error) {
	name = filepath.Base(name) // never allow directories
	if name == "" || name == "." || strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("invalid library name")
	}
	if strings.EqualFold(name, indexFileName) {
		return "", fmt.Errorf("library: %q is reserved for the library index", name)
	}
	return dedupeName(entries, name), nil
}

func (s *Store) record(entries []*Entry, name, source string, size int64) (*Entry, error) {
	id, err := newID()
	if err != nil {
		os.Remove(filepath.Join(s.dir, name))
		return nil, err
	}
	e := &Entry{
		ID: id, Name: name, Source: source,
		Size: size, AddedAt: time.Now(),
	}
	entries = append(entries, e)
	if err := s.writeIndex(entries); err != nil {
		os.Remove(filepath.Join(s.dir, name))
		return nil, err
	}
	return e, nil
}

// List returns entries, most recently added first.
func (s *Store) List() ([]*Entry, error) {
	var out []*Entry
	err := s.withLock(func() error {
		entries, err := s.readIndex()
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].AddedAt.After(entries[j].AddedAt) })
		out = entries
		return nil
	})
	return out, err
}

// Resolve maps an id or name to the entry and its absolute path. Name
// matching is case-insensitive so entries stay unambiguous on
// case-insensitive filesystems (default on macOS).
func (s *Store) Resolve(idOrName string) (*Entry, string, error) {
	var entry *Entry
	var path string
	err := s.withLock(func() error {
		entries, err := s.readIndex()
		if err != nil {
			return err
		}
		entry = findEntry(entries, idOrName)
		if entry == nil {
			return fmt.Errorf("%w: %q", ErrNotFound, idOrName)
		}
		path = filepath.Join(s.dir, entry.Name)
		return nil
	})
	return entry, path, err
}

// Remove deletes the entry's file and its index record.
func (s *Store) Remove(idOrName string) error {
	return s.withLock(func() error {
		entries, err := s.readIndex()
		if err != nil {
			return err
		}
		keep := entries[:0]
		var removed *Entry
		for _, e := range entries {
			if removed == nil && (e.ID == idOrName || strings.EqualFold(e.Name, idOrName)) {
				removed = e
				continue
			}
			keep = append(keep, e)
		}
		if removed == nil {
			return fmt.Errorf("%w: %q", ErrNotFound, idOrName)
		}
		if err := s.writeIndex(keep); err != nil {
			return err
		}
		// The file may already be gone; the index is the source of truth.
		_ = os.Remove(filepath.Join(s.dir, removed.Name))
		return nil
	})
}

// findEntry locates an entry by exact id, then by case-insensitive name.
func findEntry(entries []*Entry, idOrName string) *Entry {
	var byName *Entry
	for _, e := range entries {
		if e.ID == idOrName {
			return e
		}
		if byName == nil && strings.EqualFold(e.Name, idOrName) {
			byName = e
		}
	}
	return byName
}

func (s *Store) readIndex() ([]*Entry, error) {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		return nil, fmt.Errorf("read library index: %w", err)
	}
	var entries []*Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode library index: %w", err)
	}
	return entries, nil
}

func (s *Store) writeIndex(entries []*Entry) error {
	if entries == nil {
		entries = []*Entry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode library index: %w", err)
	}
	return writeFileAtomic(s.indexPath(), data, 0o600)
}

// ArtifactFileName returns the default library filename for an artifact
// with the given title and type ("markdown" or "html").
func ArtifactFileName(title, artifactType string) string {
	ext := ".md"
	if artifactType == "html" {
		ext = ".html"
	}
	return slugify(title) + ext
}

// slugify turns a title into a safe filename stem.
func slugify(title string) string {
	lower := strings.ToLower(title)
	var b strings.Builder
	dash := false
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteRune('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "artifact"
	}
	return s
}

// dedupeName appends "-2", "-3", ... before the extension when name is taken.
// Comparison is case-insensitive: on case-insensitive filesystems (the macOS
// default) "Report.md" and "report.md" are the same file, so treating them as
// distinct would let one entry silently overwrite another's bytes.
func dedupeName(entries []*Entry, name string) string {
	taken := map[string]bool{}
	for _, e := range entries {
		taken[strings.ToLower(e.Name)] = true
	}
	if !taken[strings.ToLower(name)] {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

// copyFileAtomic copies src to dst via a temp file plus rename. The copy is
// bounded by maxUploadBytes: a file that grows between the pre-copy stat and
// the copy is rejected instead of exceeding the promised limit. It returns
// the actual bytes copied so the index never records a stale size.
func copyFileAtomic(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open source: %w", err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	n, err := io.Copy(tmp, io.LimitReader(in, maxUploadBytes+1))
	if err != nil {
		tmp.Close()
		return 0, fmt.Errorf("copy file: %w", err)
	}
	if n > maxUploadBytes {
		tmp.Close()
		return 0, fmt.Errorf("file exceeds %d bytes", maxUploadBytes)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return 0, fmt.Errorf("rename temp file: %w", err)
	}
	return n, nil
}

// writeFileAtomic writes via a temp file plus rename.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
