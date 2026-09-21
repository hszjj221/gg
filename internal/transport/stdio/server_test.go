package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/transport/jsonrpc"
)

type fakeProvider struct{}

func (fakeProvider) Complete(context.Context, agent.Request, func(agent.Event)) (agent.AssistantMessage, error) {
	return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "ok"}, StopReason: agent.StopReasonEndTurn}, nil
}

func TestServerProcessesNewlineDelimitedRequests(t *testing.T) {
	root := t.TempDir()
	workspace, err := app.NewWorkspace(app.WorkspaceOptions{
		Config:          config.Config{CWD: filepath.Join(root, "project"), Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return fakeProvider{} },
		Repository:      session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"session.create\",\"params\":{\"name\":\"desktop\"}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"session.list\"}\n")
	var output bytes.Buffer
	server := NewServer(jsonrpc.NewHandler(workspace), input, &output)
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
