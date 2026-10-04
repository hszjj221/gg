package cliapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
)

type lifecycleProvider struct{}

func (lifecycleProvider) Complete(context.Context, agent.Request, func(agent.Event)) (agent.AssistantMessage, error) {
	return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "done"}, StopReason: agent.StopReasonEndTurn}, nil
}

// MCP closes a streamable HTTP session with DELETE. Checking that request
// verifies actual resource release rather than the presence of a defer.
func TestCLIAndDefinitionDiscoveryCloseMCPSessions(t *testing.T) {
	closed := make(chan struct{}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			closed <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodPost:
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(request.ID) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "lifecycle-test")
			var result any
			switch request.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": request.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "test", "version": "1"}}
			case "tools/list":
				result = map[string]any{"tools": []any{}}
			case "server/discover":
				// Discovery is an optional SDK probe; this fixture serves the
				// initialized session directly and does not implement it.
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
				return
			default:
				t.Errorf("unexpected MCP method %q", request.Method)
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
				t.Error(err)
			}
		}
	}))
	defer server.Close()
	for _, mode := range []string{"prompt", "manual job", "definitions"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			var stdout, stderr strings.Builder
			opts := jobTestOptions(dir, &stdout, &stderr)
			opts.ProviderFactory = func(config.Config) agent.Provider { return lifecycleProvider{} }
			mcpDir := filepath.Join(opts.HomeDir, ".gg")
			if err := os.MkdirAll(mcpDir, 0o700); err != nil {
				t.Fatal(err)
			}
			data := fmt.Sprintf(`{"servers":{"test":{"url":%q}}}`, server.URL)
			if err := os.WriteFile(filepath.Join(mcpDir, "mcp.json"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var code int
			switch mode {
			case "prompt":
				code = Run(ctx, []string{"--no-skills", "--no-memory", "-p", "hello"}, opts)
			case "manual job":
				if code := Run(ctx, []string{"job", "add", "--in", "1h", "--name", "lifecycle", "hello"}, opts); code != 0 {
					t.Fatalf("create job: %s", stderr.String())
				}
				code = Run(ctx, []string{"job", "run", "lifecycle"}, opts)
			case "definitions":
				app.RegistryToolDefinitions(ctx, config.Config{HomeDir: opts.HomeDir, CWD: opts.CWD, Artifacts: config.ArtifactConfig{Dir: filepath.Join(mcpDir, "artifacts")}, Connectors: config.ConnectorConfig{Dir: filepath.Join(mcpDir, "connectors")}})
			}
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr.String())
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatalf("MCP session not closed: %s", stderr.String())
			}
		})
	}
}
