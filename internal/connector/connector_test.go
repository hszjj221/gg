package connector

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "connectors"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreRoundTrip(t *testing.T) {
	s := openTestStore(t)
	tok := Token{
		AccessToken:  "at",
		RefreshToken: "rt",
		Expiry:       time.Now().Add(time.Hour),
		Scopes:       []string{"a", "b"},
	}
	if err := s.Save("google", tok); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("google")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at" || got.RefreshToken != "rt" || len(got.Scopes) != 2 {
		t.Fatalf("round trip = %+v", got)
	}
	// Owner-only permissions.
	fi, err := os.Stat(filepath.Join(s.Dir(), "google.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token perm = %o, want 600", fi.Mode().Perm())
	}
}

func TestStoreNotConnected(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Load("google"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
	if err := s.Remove("google"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("list = %v", names)
	}
}

func TestStoreListAndRemove(t *testing.T) {
	s := openTestStore(t)
	tok := Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := s.Save("google", tok); err != nil {
		t.Fatal(err)
	}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "google" {
		t.Fatalf("list = %v", names)
	}
	if err := s.Remove("google"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("google"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected after remove, got %v", err)
	}
}

func TestStoreRejectsBadName(t *testing.T) {
	s := openTestStore(t)
	tok := Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := s.Save("../evil", tok); err == nil {
		t.Fatal("expected error for path-traversal name")
	}
}

func TestTokenExpired(t *testing.T) {
	fresh := Token{Expiry: time.Now().Add(time.Hour)}
	if fresh.Expired() {
		t.Fatal("fresh token reports expired")
	}
	stale := Token{Expiry: time.Now().Add(-time.Minute)}
	if !stale.Expired() {
		t.Fatal("stale token reports fresh")
	}
	// 60s clock-skew margin.
	edge := Token{Expiry: time.Now().Add(30 * time.Second)}
	if !edge.Expired() {
		t.Fatal("token within skew margin should count as expired")
	}
}
