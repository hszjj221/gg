// Package standingauth persists explicit, bounded user authorizations.
package standingauth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Scope is deliberately structured: a goal or a source quote is never parsed
// into permission. Arguments limits are exact top-level JSON value matches.
type Scope struct {
	Tools        []string                   `json:"tools"`
	Consequences []string                   `json:"consequences"` // E, M, U; empty explicitly allows none
	Arguments    map[string]json.RawMessage `json:"arguments"`
}

type Grant struct {
	ID          string    `json:"id"`
	Subject     string    `json:"subject"` // explicitly names actor and user/account
	Scope       Scope     `json:"scope"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	SourceQuote string    `json:"sourceQuote"`
	Notes       string    `json:"notes,omitempty"`
}

// Validate also runs on reads, so incomplete hand-edited grants cannot match.
// Expired grants remain listable; Add separately requires a future expiry.
func (g Grant) Validate() error {
	scope, _ := json.Marshal(g.Scope)
	for _, field := range []struct{ name, value string }{
		{"subject (actor and user/account)", g.Subject}, {"source quote", g.SourceQuote},
		{"scope", string(scope)},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("standing authorization requires %s", field.name)
		}
		lower := strings.ToLower(field.value)
		for _, vague := range []string{"你看着办", "随便", "you figure it out", "do what you think is best", "whatever", "as you like"} {
			if strings.Contains(lower, vague) {
				return fmt.Errorf("vague authorization is not a grant; specify subject, tool/resource/effect limits, expiry, and an explicit source quote")
			}
		}
	}
	if g.ExpiresAt.IsZero() {
		return fmt.Errorf("standing authorization requires TTL or expiry")
	}
	if len(g.Scope.Tools) == 0 {
		return fmt.Errorf("scope requires an exact tool allowlist (--tools)")
	}
	for _, tool := range g.Scope.Tools {
		if strings.TrimSpace(tool) == "" || strings.ContainsAny(tool, "*?[] \t\r\n") {
			return fmt.Errorf("scope tools must be exact names, without wildcards or spaces")
		}
	}
	if g.Scope.Consequences == nil {
		return fmt.Errorf("scope requires explicit effects (--consequences E,M,U or none)")
	}
	for _, dim := range g.Scope.Consequences {
		if dim != "E" && dim != "M" && dim != "U" {
			return fmt.Errorf("scope consequences must be E, M, or U (or an empty list for none)")
		}
	}
	if len(g.Scope.Arguments) == 0 {
		return fmt.Errorf("scope requires resource/effect limits: --arguments must be a nonempty JSON object of exact argument values; a broad goal is not a scope")
	}
	for key, value := range g.Scope.Arguments {
		if strings.TrimSpace(key) == "" || !json.Valid(value) || string(value) == "null" {
			return fmt.Errorf("scope argument limits require nonempty keys and non-null JSON values")
		}
		var text string
		if json.Unmarshal(value, &text) == nil && strings.TrimSpace(text) == "" {
			return fmt.Errorf("scope argument limits cannot be empty strings")
		}
	}
	return nil
}

type Store struct{ dir string }

func DefaultDir(home string) string { return filepath.Join(home, ".gg", "standing-auth") }

// New does not touch disk. An absent store is an ordinary runtime miss.
func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) ensureDir() error {
	if s.dir == "" {
		return fmt.Errorf("standing authorization directory is required")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.dir, 0o700)
}

// Each grant has its own atomically published file, avoiding lost updates when
// the CLI adds/revokes while a daemon reads. No shared read-modify-write file.
func (s *Store) Add(g Grant) (Grant, error) {
	if err := g.Validate(); err != nil {
		return Grant{}, err
	}
	now := time.Now().UTC()
	if !g.ExpiresAt.After(now) {
		return Grant{}, fmt.Errorf("expiry must be in the future")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Grant{}, err
	}
	g.ID, g.CreatedAt = hex.EncodeToString(id[:]), now
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return Grant{}, err
	}
	if err := s.ensureDir(); err != nil {
		return Grant{}, err
	}
	f, err := os.CreateTemp(s.dir, ".grant-")
	if err != nil {
		return Grant{}, err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(data, '\n'))
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return Grant{}, err
	}
	if err := os.Rename(f.Name(), filepath.Join(s.dir, g.ID+".json")); err != nil {
		return Grant{}, err
	}
	return g, nil
}

func (s *Store) List() ([]Grant, error) {
	entries, err := os.ReadDir(s.dir) // sorted by ID for deterministic matching
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var grants []Grant
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if os.IsNotExist(err) { // concurrently revoked
			continue
		}
		if err != nil {
			return nil, err
		}
		var g Grant
		if err := json.Unmarshal(data, &g); err != nil {
			return nil, fmt.Errorf("read standing grant %s: %w", entry.Name(), err)
		}
		if !validID(g.ID) || entry.Name() != g.ID+".json" || g.CreatedAt.IsZero() || !g.ExpiresAt.After(g.CreatedAt) {
			return nil, fmt.Errorf("invalid standing grant metadata: %s", entry.Name())
		}
		if err := g.Validate(); err != nil {
			return nil, fmt.Errorf("invalid standing grant %s: %w", g.ID, err)
		}
		grants = append(grants, g)
	}
	return grants, nil
}

func validID(id string) bool {
	data, err := hex.DecodeString(id)
	return err == nil && len(data) == 16
}

// Revoke is idempotent. A call already approved may still finish executing.
func (s *Store) Revoke(id string) error {
	if !validID(id) {
		return fmt.Errorf("invalid standing grant id")
	}
	err := os.Remove(filepath.Join(s.dir, id+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
