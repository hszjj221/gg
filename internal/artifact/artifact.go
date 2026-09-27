// Package artifact persists versioned agent-produced deliverables
// (documents, pages) under ~/.gg/artifacts/<id>/.
package artifact

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/hszjj221/gg/internal/filelock"
)

// ErrNotFound is returned when an artifact id does not exist.
var ErrNotFound = errors.New("artifact not found")

// ErrVersionChanged is returned by Publish when the artifact gained a new
// version between the caller reading it and marking it published; the caller
// should re-read and retry instead of marking the wrong version.
var ErrVersionChanged = errors.New("artifact version changed during publish")

// Supported artifact types. Keep the set small: rendering is type-driven.
const (
	TypeMarkdown = "markdown"
	TypeHTML     = "html"
)

// maxContentBytes caps a single version so agent-produced artifacts cannot
// blow up the store (or the context when read back).
const maxContentBytes = 1 << 20 // 1 MiB

// Artifact is the metadata record for one deliverable; content lives in
// immutable v<n>.<ext> files next to artifact.json.
type Artifact struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Type             string    `json:"type"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Version          int       `json:"version"`
	PublishedVersion int       `json:"published_version"`
	Session          string    `json:"session,omitempty"`
}

// Store persists artifacts under dir.
type Store struct {
	dir string
}

// Open creates dir (0700) when missing and tightens permissions on an
// existing one, mirroring the other private stores.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700) // best-effort, like the scheduler store
	return &Store{dir: dir}, nil
}

// Dir returns the store root.
func (s *Store) Dir() string { return s.dir }

func (s *Store) lockPath() string { return filepath.Join(s.dir, "artifacts.lock") }

// withLock serializes read-modify-write cycles across processes (the CLI and
// the daemon) so concurrent edits cannot allocate the same version number.
func (s *Store) withLock(fn func() error) error {
	unlock, err := filelock.Lock(s.lockPath())
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func extFor(typ string) (string, error) {
	switch typ {
	case TypeMarkdown:
		return "md", nil
	case TypeHTML:
		return "html", nil
	default:
		return "", fmt.Errorf("unsupported artifact type %q (want markdown or html)", typ)
	}
}

func newID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate artifact id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Create stores content as version 1 of a new artifact.
func (s *Store) Create(title, typ, content string) (*Artifact, error) {
	var out *Artifact
	err := s.withLock(func() error {
		var err error
		out, err = s.create(title, typ, content)
		return err
	})
	return out, err
}

func (s *Store) create(title, typ, content string) (*Artifact, error) {
	if title == "" {
		return nil, fmt.Errorf("artifact title is required")
	}
	ext, err := extFor(typ)
	if err != nil {
		return nil, err
	}
	if len(content) > maxContentBytes {
		return nil, fmt.Errorf("artifact content exceeds %d bytes", maxContentBytes)
	}
	if content == "" {
		return nil, fmt.Errorf("artifact content is empty")
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	a := &Artifact{
		ID: id, Title: title, Type: typ,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	adir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(adir, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}
	if err := s.writeVersion(adir, 1, ext, content); err != nil {
		return nil, err
	}
	if err := s.writeMeta(adir, a); err != nil {
		return nil, err
	}
	return a, nil
}

// AddVersion appends content as a new immutable version.
func (s *Store) AddVersion(id, content string) (*Artifact, error) {
	var out *Artifact
	err := s.withLock(func() error {
		var err error
		out, err = s.addVersion(id, content)
		return err
	})
	return out, err
}

func (s *Store) addVersion(id, content string) (*Artifact, error) {
	if len(content) > maxContentBytes {
		return nil, fmt.Errorf("artifact content exceeds %d bytes", maxContentBytes)
	}
	if content == "" {
		return nil, fmt.Errorf("artifact content is empty")
	}
	a, adir, err := s.load(id)
	if err != nil {
		return nil, err
	}
	ext, err := extFor(a.Type)
	if err != nil {
		return nil, err
	}
	a.Version++
	a.UpdatedAt = time.Now()
	if err := s.writeVersion(adir, a.Version, ext, content); err != nil {
		return nil, err
	}
	if err := s.writeMeta(adir, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Get returns the metadata for id.
func (s *Store) Get(id string) (*Artifact, error) {
	var out *Artifact
	err := s.withLock(func() error {
		var err error
		out, _, err = s.load(id)
		return err
	})
	return out, err
}

// List returns all artifacts, most recently updated first. The result is
// never nil so JSON callers serialize an empty store as [] rather than null.
func (s *Store) List() ([]*Artifact, error) {
	var out []*Artifact
	err := s.withLock(func() error {
		entries, err := os.ReadDir(s.dir)
		if err != nil {
			return fmt.Errorf("list artifacts: %w", err)
		}
		out = []*Artifact{}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			a, _, err := s.load(e.Name())
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					continue
				}
				return err
			}
			out = append(out, a)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
		return nil
	})
	return out, err
}

// ReadVersion returns the content of version v (1-based).
func (s *Store) ReadVersion(id string, v int) (string, error) {
	var out string
	err := s.withLock(func() error {
		var err error
		out, err = s.readVersion(id, v)
		return err
	})
	return out, err
}

func (s *Store) readVersion(id string, v int) (string, error) {
	a, adir, err := s.load(id)
	if err != nil {
		return "", err
	}
	if v < 1 || v > a.Version {
		return "", fmt.Errorf("artifact %s has no version %d", id, v)
	}
	ext, err := extFor(a.Type)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(adir, fmt.Sprintf("v%d.%s", v, ext)))
	if err != nil {
		return "", fmt.Errorf("read artifact version: %w", err)
	}
	return string(data), nil
}

// Publish marks expectedVersion as published and returns its content.
// When the artifact gained a newer version since the caller read it,
// Publish returns ErrVersionChanged instead of marking a version whose
// bytes were never copied — the caller should re-read and retry.
func (s *Store) Publish(id string, expectedVersion int) (content string, version int, err error) {
	err = s.withLock(func() error {
		a, adir, err := s.load(id)
		if err != nil {
			return err
		}
		if expectedVersion > 0 && a.Version != expectedVersion {
			return fmt.Errorf("%w: have v%d, want v%d", ErrVersionChanged, a.Version, expectedVersion)
		}
		content, err = s.readVersion(id, a.Version)
		if err != nil {
			return err
		}
		version = a.Version
		a.PublishedVersion = a.Version
		a.UpdatedAt = time.Now()
		return s.writeMeta(adir, a)
	})
	return content, version, err
}

// Remove deletes the artifact and all its versions.
func (s *Store) Remove(id string) error {
	return s.withLock(func() error {
		if _, _, err := s.load(id); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(s.dir, id)); err != nil {
			return fmt.Errorf("remove artifact: %w", err)
		}
		return nil
	})
}

func (s *Store) load(id string) (*Artifact, string, error) {
	if id == "" || id != filepath.Base(id) {
		return nil, "", fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	adir := filepath.Join(s.dir, id)
	data, err := os.ReadFile(filepath.Join(adir, "artifact.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		return nil, "", fmt.Errorf("read artifact: %w", err)
	}
	var a Artifact
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, "", fmt.Errorf("decode artifact: %w", err)
	}
	return &a, adir, nil
}

func (s *Store) writeMeta(adir string, a *Artifact) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("encode artifact: %w", err)
	}
	return writeFileAtomic(filepath.Join(adir, "artifact.json"), data, 0o600)
}

func (s *Store) writeVersion(adir string, v int, ext, content string) error {
	return writeFileAtomic(filepath.Join(adir, fmt.Sprintf("v%d.%s", v, ext)), []byte(content), 0o600)
}

// writeFileAtomic writes via a temp file plus rename so readers never see a
// partial version.
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
