package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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

func TestHTTPHandlerHealthzNeedsNoAuth(t *testing.T) {
	handler := testHandler(t, "secret")
	// No Authorization header: /healthz is the unauthenticated liveness probe.
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("healthz response: status=%d body=%s", response.Code, response.Body.String())
	}
	// /health stays behind the bearer-token gate.
	request = httptest.NewRequest(http.MethodGet, "/health", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("health without token: status=%d, want unauthorized", response.Code)
	}
	// /healthz rejects non-GET.
	request = httptest.NewRequest(http.MethodPost, "/healthz", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("healthz POST: status=%d, want 405", response.Code)
	}
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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) || !strings.Contains(response.Body.String(), `"protocolVersion":"`+jsonrpc.ProtocolVersion+`"`) {
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

func TestHTTPHandlerCORSPreflightForLocalUI(t *testing.T) {
	handler := testHandler(t, "secret")

	// Packaged Electron loads the renderer from file:// (origin "null") and
	// sends Authorization + JSON headers, so the preflight carries no token
	// and must be answered outside the bearer-token gate.
	request := httptest.NewRequest(http.MethodOptions, "/rpc", nil)
	request.Header.Set("Origin", "null")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "null" {
		t.Fatalf("preflight ACAO = %q, want null", got)
	}
	for _, header := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if response.Header().Get(header) == "" {
			t.Fatalf("preflight missing %s", header)
		}
	}

	// A foreign origin must not get a preflight answer; it falls through to
	// the bearer-token gate.
	request = httptest.NewRequest(http.MethodOptions, "/rpc", nil)
	request.Header.Set("Origin", "https://evil.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("foreign preflight status = %d, want 401", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("foreign preflight ACAO = %q, want empty", got)
	}

	// Authenticated responses to local UI origins carry ACAO; non-browser
	// clients (no Origin) get no CORS headers.
	for _, origin := range []string{"null", "http://127.0.0.1:5173", "http://localhost:3000"} {
		body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"session.create","params":{"name":"web"}}`)
		request := httptest.NewRequest(http.MethodPost, "/rpc", body)
		request.Header.Set("Authorization", "Bearer secret")
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("rpc with origin %q: status = %d, want 200", origin, response.Code)
		}
		if got := response.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("rpc with origin %q: ACAO = %q", origin, got)
		}
	}
	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"session.create","params":{"name":"web"}}`)
	request = httptest.NewRequest(http.MethodPost, "/rpc", body)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("rpc without origin: status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("rpc without origin: ACAO = %q, want empty", got)
	}
}

func TestIsLocalUIOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   bool
	}{
		{"null", true}, // packaged Electron loads the renderer from file://
		{"http://127.0.0.1:5173", true},
		{"http://localhost:3000", true},
		{"https://localhost:8443", true},
		{"http://[::1]:5173", true},
		{"https://evil.example", false},
		{"http://127.0.0.1.evil.example", false},
		{"ftp://127.0.0.1/x", false},
		{"not-a-url", false},
		{"", false},
	} {
		if got := isLocalUIOrigin(tc.origin); got != tc.want {
			t.Errorf("isLocalUIOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
		}
	}
}

func TestHTTPEventStreamUsesStableApplicationErrors(t *testing.T) {
	handler := testHandler(t, "secret")
	request := httptest.NewRequest(http.MethodGet, "/events?runId=missing", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":"run_not_found"`) {
		t.Fatalf("event error response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPEventStreamUsesLastEventID(t *testing.T) {
	handler := testHandler(t, "secret")
	request := httptest.NewRequest(http.MethodGet, "/events?runId=missing", nil)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Last-Event-ID", "not-a-number")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want bad request", response.Code)
	}
}

func TestHTTPEventStreamResumesAfterLastEventID(t *testing.T) {
	handler := testHandler(t, "secret")
	snapshot, err := handler.rt.CreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	run, err := handler.rt.StartTurn(context.Background(), snapshot.SessionID, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/events?runId="+run.ID(), nil)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Last-Event-ID", "1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Contains(body, "id: 1\n") || !strings.Contains(body, "id: 2\n") || !strings.Contains(body, `"type":"run_completed"`) {
		t.Fatalf("unexpected resumed event stream: status=%d body=%s", response.Code, body)
	}
}

func TestWriteSSEIncludesEventID(t *testing.T) {
	var output bytes.Buffer
	writeSSE(&output, "event", "7", map[string]bool{"ok": true})
	if got := output.String(); got != "id: 7\nevent: event\ndata: {\"ok\":true}\n\n" {
		t.Fatalf("unexpected SSE frame: %q", got)
	}
}

func testHandler(t *testing.T, token string) *Handler {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := workspace.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CWD: cwd, Selection: "test:model"}
	rt, err := app.NewRuntime(app.RuntimeOptions{
		Config:            cfg,
		ProviderFactory:   func(config.Config) agent.Provider { return fakeProvider{} },
		WorkspaceRegistry: reg,
		NoSkills:          true,
		Repository:        session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	rpc := jsonrpc.NewHandlerWithContext(context.Background(), rt)
	return NewHandler(rpc, rt, token)
}
