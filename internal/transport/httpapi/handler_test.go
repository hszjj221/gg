package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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

func TestHTTPHandlerRequiresBearerToken(t *testing.T) {
	handler := testHandler(t, "secret")
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want unauthorized", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("health response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPHandlerServesJSONRPC(t *testing.T) {
	handler := testHandler(t, "secret")
	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"session.create","params":{"name":"web"}}`)
	request := httptest.NewRequest(http.MethodPost, "/rpc", body)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sessionName":"web"`) {
		t.Fatalf("rpc response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func testHandler(t *testing.T, token string) *Handler {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{CWD: filepath.Join(root, "project"), Selection: "test:model"}
	workspace, err := app.NewWorkspace(app.WorkspaceOptions{
		Config:          cfg,
		ProviderFactory: func(config.Config) agent.Provider { return fakeProvider{} },
		Repository:      session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	rpc := jsonrpc.NewHandlerWithContext(context.Background(), workspace)
	return NewHandler(rpc, workspace, token)
}
