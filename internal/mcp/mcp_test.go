package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hszjj221/gg/internal/agent"
)

// fakeSession is a test double for the MCP client session.
type fakeSession struct {
	tools   []*mcp.Tool
	listErr error
	callErr error
	callRes *mcp.CallToolResult
	calls   []string
	closed  bool
}

func (f *fakeSession) ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &mcp.ListToolsResult{Tools: f.tools}, nil
}

func (f *fakeSession) CallTool(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	f.calls = append(f.calls, params.Name)
	if f.callErr != nil {
		return nil, f.callErr
	}
	return f.callRes, nil
}

func (f *fakeSession) Close() error {
	f.closed = true
	return nil
}

func textTool(name, desc string, schema map[string]any) *mcp.Tool {
	t := &mcp.Tool{Name: name, Description: desc}
	if schema != nil {
		t.InputSchema = schema
	}
	return t
}

func TestConfigLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "mcp.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("expected no servers, got %v", cfg.Servers)
	}
}

func TestConfigLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	data := `{"servers": {
		"fs": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem"], "env": {"FOO": "bar"}},
		"web": {"url": "https://example.com/mcp"}
	}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fs := cfg.Servers["fs"]
	if fs.Command != "npx" || len(fs.Args) != 2 || fs.Env["FOO"] != "bar" {
		t.Fatalf("stdio server parsed wrong: %+v", fs)
	}
	if cfg.Servers["web"].URL != "https://example.com/mcp" {
		t.Fatalf("http server parsed wrong: %+v", cfg.Servers["web"])
	}
}

func TestConfigLoadInvalid(t *testing.T) {
	cases := map[string]string{
		"bad json":        `{"servers": }`,
		"both transports": `{"servers": {"s": {"command": "x", "url": "https://x"}}}`,
		"no transport":    `{"servers": {"s": {}}}`,
		"empty name":      `{"servers": {"": {"command": "x"}}}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSanitizeToolName(t *testing.T) {
	cases := []struct{ server, tool, want string }{
		{"filesystem", "read_file", "mcp_filesystem_read_file"},
		{"my-server", "read.file", "mcp_my-server_read_file"},
		{"srv", "a/b:c", "mcp_srv_a_b_c"},
		{"", "", "mcp_server_tool"},
	}
	for _, c := range cases {
		if got := SanitizeToolName(c.server, c.tool); got != c.want {
			t.Errorf("SanitizeToolName(%q,%q) = %q, want %q", c.server, c.tool, got, c.want)
		}
	}
	long := SanitizeToolName("server-with-a-very-long-name-for-testing", "tool-with-a-very-long-name-for-testing")
	if len(long) > maxToolNameLength {
		t.Errorf("name too long: %d chars", len(long))
	}
	for _, r := range long {
		if !isNameChar(r) {
			t.Errorf("invalid char %q in %q", r, long)
		}
	}
}

func isNameChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

func TestNormalizeInputSchema(t *testing.T) {
	// nil → empty object schema
	got := NormalizeInputSchema(nil)
	if got["type"] != "object" {
		t.Errorf("nil schema type = %v", got["type"])
	}
	if props, ok := got["properties"].(map[string]any); !ok || len(props) != 0 {
		t.Errorf("nil schema properties = %v", got["properties"])
	}

	// missing type/properties get defaults; unknown keywords pass through
	got = NormalizeInputSchema(map[string]any{
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
		"oneOf":      []any{map[string]any{"type": "string"}},
	})
	if got["type"] != "object" {
		t.Errorf("type = %v", got["type"])
	}
	if _, ok := got["oneOf"]; !ok {
		t.Error("oneOf should pass through untouched")
	}

	// garbage → safe fallback
	got = NormalizeInputSchema("not-a-schema")
	if got["type"] != "object" {
		t.Errorf("garbage schema type = %v", got["type"])
	}

	// long descriptions are truncated, recursively
	longRunes := make([]rune, 2000)
	for i := range longRunes {
		longRunes[i] = 'x'
	}
	got = NormalizeInputSchema(map[string]any{
		"description": string(longRunes),
		"properties": map[string]any{
			"p": map[string]any{"description": string(longRunes)},
		},
	})
	if n := len([]rune(got["description"].(string))); n != descriptionTruncateRunes {
		t.Errorf("top description runes = %d, want %d", n, descriptionTruncateRunes)
	}
	props := got["properties"].(map[string]any)
	if n := len([]rune(props["p"].(map[string]any)["description"].(string))); n != descriptionTruncateRunes {
		t.Errorf("nested description runes = %d, want %d", n, descriptionTruncateRunes)
	}
}

func TestMCPToolDefinition(t *testing.T) {
	sess := &fakeSession{}
	tool := newMCPTool("fs", textTool("read_file", "reads a file", map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
	}), "mcp_fs_read_file", sess)
	def := tool.Definition()
	if def.Name != "mcp_fs_read_file" || def.Description != "reads a file" {
		t.Fatalf("bad definition: %+v", def)
	}
	if def.Parameters["type"] != "object" {
		t.Fatalf("bad parameters: %v", def.Parameters)
	}
	if tool.Name() != "mcp_fs_read_file" {
		t.Fatalf("Name() = %q", tool.Name())
	}
}

func TestMCPToolExecute(t *testing.T) {
	sess := &fakeSession{callRes: &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "hello"}},
	}}
	tool := newMCPTool("fs", textTool("read_file", "", nil), "mcp_fs_read_file", sess)
	res := tool.Execute(context.Background(), json.RawMessage(`{"path":"/x"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}
	if len(sess.calls) != 1 || sess.calls[0] != "read_file" {
		t.Fatalf("called %v, want [read_file]", sess.calls)
	}
	if got := res.Content[0].Text; got != "hello" {
		t.Fatalf("text = %q", got)
	}
}

func TestMCPToolExecuteErrorResult(t *testing.T) {
	sess := &fakeSession{callRes: &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "boom"}},
	}}
	tool := newMCPTool("fs", textTool("read_file", "", nil), "mcp_fs_read_file", sess)
	res := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("expected IsError")
	}
}

func TestMCPToolExecuteNonTextContent(t *testing.T) {
	sess := &fakeSession{callRes: &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.ImageContent{}},
	}}
	tool := newMCPTool("fs", textTool("shot", "", nil), "mcp_fs_shot", sess)
	res := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.IsError {
		t.Fatalf("non-text content should not error: %v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "non-text content") {
		t.Fatalf("placeholder = %q", res.Content[0].Text)
	}
}

func TestMCPToolExecuteBadArgs(t *testing.T) {
	sess := &fakeSession{}
	tool := newMCPTool("fs", textTool("read_file", "", nil), "mcp_fs_read_file", sess)
	res := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	if !res.IsError {
		t.Fatal("expected error for invalid args JSON")
	}
	if len(sess.calls) != 0 {
		t.Fatal("server should not be called with invalid args")
	}
}

func TestMCPToolApproval(t *testing.T) {
	var _ agent.ApprovalDescriber = (*MCPTool)(nil)
	tool := newMCPTool("fs", textTool("delete", "", nil), "mcp_fs_delete", &fakeSession{})
	req, err := tool.ApprovalRequest(json.RawMessage(`{"path":"/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolName != "mcp_fs_delete" {
		t.Errorf("ToolName = %q", req.ToolName)
	}
	if !strings.Contains(req.Summary, "fs.delete") {
		t.Errorf("Summary = %q", req.Summary)
	}
}

func TestConnectorTools(t *testing.T) {
	sessFS := &fakeSession{tools: []*mcp.Tool{
		textTool("read_file", "reads", nil),
		textTool("write.file", "writes", nil),
	}}
	sessWeb := &fakeSession{tools: []*mcp.Tool{
		textTool("fetch", "fetches", nil),
	}}
	dials := 0
	c := NewConnector(writeConfig(t, `{"servers": {
		"fs": {"command": "x"},
		"web": {"url": "https://example.com"}
	}}`))
	c.dial = func(_, _ context.Context, name string, _ ServerConfig) (session, error) {
		dials++
		if name == "fs" {
			return sessFS, nil
		}
		return sessWeb, nil
	}
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("got %d tools", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	for _, want := range []string{"mcp_fs_read_file", "mcp_fs_write_file", "mcp_web_fetch"} {
		if !names[want] {
			t.Errorf("missing tool %q (got %v)", want, names)
		}
	}
	// Second call must not re-dial: connections are cached.
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dials != 2 {
		t.Errorf("dials = %d, want 2 (cached)", dials)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !sessFS.closed || !sessWeb.closed {
		t.Error("Close must close every session")
	}
}

func TestConnectorSkipsFailedServer(t *testing.T) {
	sessOK := &fakeSession{tools: []*mcp.Tool{textTool("ok", "", nil)}}
	dials := map[string]int{}
	c := NewConnector(writeConfig(t, `{"servers": {
		"bad": {"command": "missing-binary"},
		"good": {"command": "x"}
	}}`))
	c.dial = func(_, _ context.Context, name string, _ ServerConfig) (session, error) {
		dials[name]++
		if name == "bad" {
			return nil, context.DeadlineExceeded
		}
		return sessOK, nil
	}
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name() != "mcp_good_ok" {
		t.Fatalf("tools = %v", tools)
	}
	// Failed server is not retried on later calls.
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dials["bad"] != 1 {
		t.Errorf("bad server dialed %d times, want 1", dials["bad"])
	}
}

func TestConnectorRemovesServer(t *testing.T) {
	sess := &fakeSession{tools: []*mcp.Tool{textTool("t", "", nil)}}
	path := writeConfig(t, `{"servers": {"a": {"command": "x"}, "b": {"command": "y"}}}`)
	c := NewConnector(path)
	c.dial = func(_, _ context.Context, _ string, _ ServerConfig) (session, error) { return sess, nil }
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"servers": {"a": {"command": "x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools after removal", len(tools))
	}
	if !sess.closed {
		t.Error("removed server session should be closed")
	}
}

func TestConnectorNameCollision(t *testing.T) {
	mkSess := func(tool string) *fakeSession {
		return &fakeSession{tools: []*mcp.Tool{textTool(tool, "", nil)}}
	}
	// "a.b"+"c" and "a"+"b.c" both sanitize to mcp_a_b_c.
	c := NewConnector(writeConfig(t, `{"servers": {
		"a.b": {"command": "x"},
		"a": {"command": "y"}
	}}`))
	c.dial = func(_, _ context.Context, name string, _ ServerConfig) (session, error) {
		if name == "a.b" {
			return mkSess("c"), nil
		}
		return mkSess("b.c"), nil
	}
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools", len(tools))
	}
	if tools[0].Name() == tools[1].Name() {
		t.Fatalf("collision not resolved: %q", tools[0].Name())
	}
	if tools[0].Name() != "mcp_a_b_c" {
		t.Errorf("first (sorted) server should keep base name, got %q", tools[0].Name())
	}
}

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConnectorDoesNotCacheCancellationAsFailure(t *testing.T) {
	sessOK := &fakeSession{tools: []*mcp.Tool{textTool("ok", "", nil)}}
	dials := 0
	c := NewConnector(writeConfig(t, `{"servers": {"s": {"command": "x"}}}`))
	c.dial = func(initCtx, _ context.Context, _ string, _ ServerConfig) (session, error) {
		dials++
		if initCtx.Err() != nil {
			return nil, initCtx.Err()
		}
		return sessOK, nil
	}
	// First turn is cancelled while dialing: must not poison the cache.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Tools(cancelled); err != nil {
		t.Fatal(err)
	}
	// Next turn retries and succeeds.
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools after retry", len(tools))
	}
	if dials != 2 {
		t.Errorf("dials = %d, want 2 (cancelled attempt retried)", dials)
	}
}

func TestConnectorReconnectsOnConfigChange(t *testing.T) {
	var mu sync.Mutex
	sessions := []*fakeSession{}
	path := writeConfig(t, `{"servers": {"s": {"command": "v1"}}}`)
	c := NewConnector(path)
	c.dial = func(_, _ context.Context, _ string, sc ServerConfig) (session, error) {
		mu.Lock()
		defer mu.Unlock()
		s := &fakeSession{tools: []*mcp.Tool{textTool("t", sc.Command, nil)}}
		sessions = append(sessions, s)
		return s, nil
	}
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Change the server command: the old session must be closed and a new
	// one dialed.
	if err := os.WriteFile(path, []byte(`{"servers": {"s": {"command": "v2"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (reconnected)", len(sessions))
	}
	if !sessions[0].closed {
		t.Error("old session should be closed after config change")
	}
	def := tools[0].Definition()
	if def.Description != "v2" {
		t.Errorf("tool still serves old config: %q", def.Description)
	}
}

func TestConnectorListsAllPages(t *testing.T) {
	calls := 0
	inner := &fakeSession{}
	wrapper := &pagedSession{inner: inner, calls: &calls}
	c2 := NewConnector(writeConfig(t, `{"servers": {"s": {"command": "x"}}}`))
	c2.dial = func(_, _ context.Context, _ string, _ ServerConfig) (session, error) {
		return wrapper, nil
	}
	tools, err := c2.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (both pages)", len(tools))
	}
	if calls != 2 {
		t.Errorf("ListTools calls = %d, want 2 (paginated)", calls)
	}
}

// pagedSession returns a cursor on the first ListTools call.
type pagedSession struct {
	inner *fakeSession
	calls *int
}

func (p *pagedSession) ListTools(ctx context.Context, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	*p.calls++
	if params.Cursor == "" {
		return &mcp.ListToolsResult{
			Tools:      []*mcp.Tool{textTool("t1", "", nil)},
			NextCursor: "page2",
		}, nil
	}
	return &mcp.ListToolsResult{Tools: []*mcp.Tool{textTool("t2", "", nil)}}, nil
}

func (p *pagedSession) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return p.inner.CallTool(ctx, params)
}

func (p *pagedSession) Close() error { return p.inner.Close() }

func TestConnectorLifeContextCancelledOnClose(t *testing.T) {
	var lifeCtx context.Context
	c := NewConnector(writeConfig(t, `{"servers": {"s": {"command": "x"}}}`))
	c.dial = func(_, lc context.Context, _ string, _ ServerConfig) (session, error) {
		lifeCtx = lc
		return &fakeSession{tools: []*mcp.Tool{textTool("t", "", nil)}}, nil
	}
	if _, err := c.Tools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lifeCtx == nil {
		t.Fatal("dial did not receive a lifetime context")
	}
	// A turn-scoped cancellation must not kill the server lifetime ctx.
	turn, cancel := context.WithCancel(context.Background())
	cancel()
	_ = turn
	if err := lifeCtx.Err(); err != nil {
		t.Fatalf("lifetime ctx already done: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if lifeCtx.Err() == nil {
		t.Error("Close must cancel the connector lifetime context (reaps stdio)")
	}
}

func TestMCPToolEmptyErrorResult(t *testing.T) {
	sess := &fakeSession{callRes: &mcp.CallToolResult{IsError: true}}
	tool := newMCPTool("fs", textTool("bad", "", nil), "mcp_fs_bad", sess)
	res := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("expected IsError")
	}
	if !strings.Contains(res.Content[0].Text, "no content") {
		t.Fatalf("expected diagnostic, got %q", res.Content[0].Text)
	}
}
