// Package connector manages third-party service connections for gg:
// OAuth 2.0 authorization, token persistence, and refresh. Concrete
// providers (e.g. Google) build on this framework.
package connector

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hszjj221/gg/internal/filelock"
)

// ErrNotConnected is returned when no token exists for a connector.
var ErrNotConnected = errors.New("connector not connected")

// Token is the persisted OAuth2 token for one connector.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	Scopes       []string  `json:"scopes"`
	// ClientID/ClientSecret are persisted so later processes can refresh
	// without the user re-supplying --client-id. Same 0600 file as the
	// refresh token; never printed by status or logs.
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// Expired reports whether the access token should be refreshed, with a
// small clock-skew margin.
func (t Token) Expired() bool {
	return time.Until(t.Expiry) < 60*time.Second
}

// Store persists connector tokens under dir (~/.gg/connectors/).
// Tokens are owner-only (0600), matching session and memory persistence.
type Store struct {
	dir string
}

// Open creates dir (0700) when missing and tightens permissions on an
// existing one.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("connector dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create connector dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700) // best-effort
	return &Store{dir: dir}, nil
}

// Dir returns the store root.
func (s *Store) Dir() string { return s.dir }

func (s *Store) tokenPath(name string) string {
	return filepath.Join(s.dir, name+".json")
}

func (s *Store) lockPath() string { return filepath.Join(s.dir, "connectors.lock") }

func (s *Store) withLock(fn func() error) error {
	unlock, err := filelock.Lock(s.lockPath())
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// Save stores the token for a connector.
func (s *Store) Save(name string, tok Token) error {
	if name == "" || name != filepath.Base(name) {
		return fmt.Errorf("invalid connector name %q", name)
	}
	return s.withLock(func() error {
		data, err := json.MarshalIndent(tok, "", "  ")
		if err != nil {
			return fmt.Errorf("encode token: %w", err)
		}
		return writeFileAtomic(s.tokenPath(name), data, 0o600)
	})
}

// Load returns the stored token, or ErrNotConnected.
func (s *Store) Load(name string) (Token, error) {
	var tok Token
	err := s.withLock(func() error {
		data, err := os.ReadFile(s.tokenPath(name))
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %q (run `gg connect %s` first)", ErrNotConnected, name, name)
			}
			return fmt.Errorf("read token: %w", err)
		}
		if err := json.Unmarshal(data, &tok); err != nil {
			return fmt.Errorf("decode token: %w", err)
		}
		if tok.RefreshToken == "" || tok.AccessToken == "" {
			return fmt.Errorf("%w: %q has an empty token, please reconnect", ErrNotConnected, name)
		}
		return nil
	})
	return tok, err
}

// Remove deletes the connector's token (disconnect).
func (s *Store) Remove(name string) error {
	return s.withLock(func() error {
		if err := os.Remove(s.tokenPath(name)); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %q", ErrNotConnected, name)
			}
			return fmt.Errorf("remove token: %w", err)
		}
		return nil
	})
}

// List returns the names of connected connectors.
func (s *Store) List() ([]string, error) {
	var out []string
	err := s.withLock(func() error {
		entries, err := os.ReadDir(s.dir)
		if err != nil {
			return fmt.Errorf("list connectors: %w", err)
		}
		out = []string{}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if filepath.Ext(name) != ".json" {
				continue
			}
			out = append(out, name[:len(name)-len(".json")])
		}
		return nil
	})
	return out, err
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
