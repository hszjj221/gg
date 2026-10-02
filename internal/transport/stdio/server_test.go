package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/transport/jsonrpc"
	"github.com/hszjj221/gg/internal/workspace"
)

type fakeProvider struct{}

func (fakeProvider) Complete(context.Context, agent.Request, func(agent.Event)) (agent.AssistantMessage, error) {
	return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "ok"}, StopReason: agent.StopReasonEndTurn}, nil
}

func TestServerProcessesNewlineDelimitedRequests(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := workspace.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rt, err := app.NewRuntime(app.RuntimeOptions{
		Config:            config.Config{CWD: cwd, Selection: "test:model"},
		ProviderFactory:   func(config.Config) agent.Provider { return fakeProvider{} },
		WorkspaceRegistry: reg,
		NoSkills:          true,
		Repository:        session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"session.create\",\"params\":{\"name\":\"desktop\"}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"session.list\"}\n")
	var output bytes.Buffer
	server := NewServer(jsonrpc.NewHandler(rt), input, &output)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	seen := map[string]bool{}
	for {
		var response jsonrpc.Response
		if err := decoder.Decode(&response); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		seen[string(response.ID)] = response.Error == nil
	}
	if !seen["1"] || !seen["2"] {
		t.Fatalf("missing responses: %v output=%s", seen, output.String())
	}
}
