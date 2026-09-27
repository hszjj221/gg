package cliapp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
)

func TestConnectListEmpty(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)
	if code := Run(context.Background(), []string{"connect", "list"}, opts); code != 0 {
		t.Fatalf("list exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "gg connect google") {
		t.Fatalf("list = %q", stdout.String())
	}
}

func TestConnectStatusRemove(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	// Seed a connected token directly.
	store, err := connector.Open(filepath.Join(dir, "home", ".gg", "connectors"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(google.Name, connector.Token{
		AccessToken: "at", RefreshToken: "rt",
		Expiry: time.Now().Add(time.Hour), Scopes: google.Scopes,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"connect", "status"}, opts); code != 0 {
		t.Fatalf("status exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "connector: google") || !strings.Contains(stdout.String(), "gmail.readonly") {
		t.Fatalf("status = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"connect", "list"}, opts); code != 0 {
		t.Fatalf("list exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "google") {
		t.Fatalf("list = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"connect", "remove", "google"}, opts); code != 0 {
		t.Fatalf("remove exit %d: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"connect", "status", "google"}, opts); code == 0 {
		t.Fatalf("status after remove should fail, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "未连接") {
		t.Fatalf("status after remove = %q", stdout.String())
	}
}

func TestConnectGoogleNeedsClientID(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)
	// No client id anywhere: usage guidance, no browser opened.
	if code := Run(context.Background(), []string{"connect", "google"}, opts); code != 2 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Client ID") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestConnectUnknownName(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)
	if code := Run(context.Background(), []string{"connect", "status", "outlook"}, opts); code != 2 {
		t.Fatalf("exit = %d", code)
	}
}
