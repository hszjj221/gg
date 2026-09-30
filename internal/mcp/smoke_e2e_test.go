package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSmokeStdioServer exercises the real dialServer against a real MCP
// server subprocess. Not run by default (needs the echo-server binary);
// run with: GG_MCP_SMOKE=1 go test -run TestSmokeStdioServer ./internal/mcp/
func TestSmokeStdioServer(t *testing.T) {
	if os.Getenv("GG_MCP_SMOKE") == "" {
		t.Skip("set GG_MCP_SMOKE=1 to run")
	}
	bin := os.Getenv("GG_MCP_SMOKE_BIN")
	if bin == "" {
		bin = "/tmp/mcpecho/echo-server"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("echo server not built: %v", err)
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	cfg := `{"servers": {"echo": {"command": "` + bin + `", "env": {"SECRET_X": "1"}}}}`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewConnector(path)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tools, err := c.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools", len(tools))
	}
	if tools[0].Name() != "mcp_echo_echo" {
		t.Fatalf("name = %q", tools[0].Name())
	}
	res := tools[0].Execute(ctx, json.RawMessage(`{"text":"hi"}`))
	if res.IsError {
		t.Fatalf("execute error: %v", res.Content)
	}
	t.Logf("result: %q", res.Content[0].Text)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
