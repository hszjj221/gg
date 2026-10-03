package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedServerRetriesAfterConfigChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{"servers":{"demo":{"command":"old"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := NewConnector(path)
	defer c.Close()
	calls := 0
	c.dial = func(context.Context, context.Context, string, ServerConfig) (session, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("old config failed")
		}
		return &fakeSession{}, nil
	}
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("unchanged failed config was retried")
	}
	if err := os.WriteFile(path, []byte(`{"servers":{"demo":{"command":"fixed"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(c.FailedServers()) != 0 {
		t.Fatalf("expected retry after config fix: calls=%d", calls)
	}
}

func TestClosedConnectorDoesNotReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{"servers":{"demo":{"command":"test"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := NewConnector(path)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c.dial = func(context.Context, context.Context, string, ServerConfig) (session, error) {
		t.Fatal("closed connector dialed")
		return nil, nil
	}
	if _, err := c.Tools(context.Background()); err == nil {
		t.Fatal("closed connector accepted discovery")
	}
}
