package jsonrpc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

type fakeProvider struct{}

func (fakeProvider) Complete(context.Context, agent.Request, func(agent.Event)) (agent.AssistantMessage, error) {
	return agent.AssistantMessage{Message: agent.Message{Role: agent.RoleAssistant, Content: "ok"}, StopReason: agent.StopReasonEndTurn}, nil
}

type blockingProvider struct{}

func (blockingProvider) Complete(ctx context.Context, _ agent.Request, _ func(agent.Event)) (agent.AssistantMessage, error) {
	<-ctx.Done()
	return agent.AssistantMessage{}, ctx.Err()
}

func TestHandlerCreatesAndListsSessionsWithoutPaths(t *testing.T) {
	handler := NewHandler(testWorkspace(t))
	created := handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`1`), Method: "session.create", Params: []byte(`{"name":"demo"}`)})
	if created.Error != nil {
		t.Fatal(created.Error)
	}
	data, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || containsJSONKey(data, "sessionPath") {
		t.Fatalf("unexpected create response: %s", data)
	}
	listed := handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`2`), Method: "session.list"})
	if listed.Error != nil {
		t.Fatal(listed.Error)
	}
	items, ok := listed.Result.([]app.SessionSummary)
	if !ok || len(items) != 1 || items[0].Name != "demo" {
		t.Fatalf("unexpected list result: %#v", listed.Result)
	}
}

func TestHandlerReturnsProtocolErrors(t *testing.T) {
	handler := NewHandler(testWorkspace(t))
	response := handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`1`), Method: "missing"})
	if response.Error == nil || response.Error.Code != -32601 {
		t.Fatalf("unexpected missing-method response: %+v", response)
	}
	response = handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`2`), Method: "session.open", Params: []byte(`{}`)})
	if response.Error == nil || response.Error.Code != -32602 {
		t.Fatalf("unexpected invalid-params response: %+v", response)
	}
}

func TestHandlerReportsProtocolCapabilities(t *testing.T) {
	handler := NewHandler(testWorkspace(t))
	response := handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`1`), Method: "system.info"})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	info, ok := response.Result.(SystemInfo)
	if !ok || info.ProtocolVersion != ProtocolVersion || len(info.Capabilities) == 0 {
		t.Fatalf("unexpected system info: %#v", response.Result)
	}
}

func TestHandlerReturnsStableApplicationError(t *testing.T) {
	handler := NewHandler(testWorkspace(t))
	response := handler.Handle(context.Background(), Request{
		JSONRPC: Version,
		ID:      []byte(`1`),
		Method:  "session.open",
		Params:  []byte(`{"sessionId":"missing"}`),
	})
	if response.Error == nil || response.Error.Code != -32001 || response.Error.Data == nil {
		t.Fatalf("unexpected application error: %+v", response)
	}
	if response.Error.Data.Code != app.ErrorSessionNotFound || response.Error.Data.Retryable {
		t.Fatalf("unexpected application error data: %+v", response.Error.Data)
	}
}

func TestHandlerFindsActiveRunForSession(t *testing.T) {
	workspace := testWorkspaceWithProvider(t, blockingProvider{})
	handler := NewHandler(workspace)
	created := handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`1`), Method: "session.create"})
	snapshot, ok := created.Result.(app.Snapshot)
	if created.Error != nil || !ok {
		t.Fatalf("create session: %+v", created)
	}
	started := handler.Handle(context.Background(), Request{
		JSONRPC: Version,
		ID:      []byte(`2`),
		Method:  "run.start",
		Params:  json.RawMessage(`{"sessionId":"` + snapshot.SessionID + `","prompt":"hello"}`),
	})
	startResult, ok := started.Result.(map[string]string)
	if started.Error != nil || !ok || startResult["runId"] == "" {
		t.Fatalf("start run: %+v", started)
	}
	active := handler.Handle(context.Background(), Request{
		JSONRPC: Version,
		ID:      []byte(`3`),
		Method:  "run.active",
		Params:  json.RawMessage(`{"sessionId":"` + snapshot.SessionID + `"}`),
	})
	status, ok := active.Result.(*app.RunStatus)
	if active.Error != nil || !ok || status.ID != startResult["runId"] || status.Done {
		t.Fatalf("active run: %+v", active)
	}
	got := handler.Handle(context.Background(), Request{
		JSONRPC: Version,
		ID:      []byte(`4`),
		Method:  "run.get",
		Params:  json.RawMessage(`{"runId":"` + status.ID + `"}`),
	})
	if runStatus, ok := got.Result.(app.RunStatus); got.Error != nil || !ok || runStatus.ID != status.ID {
		t.Fatalf("get run: %+v", got)
	}
	if response := handler.Handle(context.Background(), Request{
		JSONRPC: Version,
		ID:      []byte(`5`),
		Method:  "run.cancel",
		Params:  json.RawMessage(`{"runId":"` + status.ID + `"}`),
	}); response.Error != nil {
		t.Fatalf("cancel run: %+v", response)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var after int64
	for {
		events, done, err := workspace.WaitRun(ctx, status.ID, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) > 0 {
			after = events[len(events)-1].Sequence
		}
		if done {
			break
		}
	}
}

func testWorkspace(t *testing.T) *app.Workspace {
	return testWorkspaceWithProvider(t, fakeProvider{})
}

func testWorkspaceWithProvider(t *testing.T, provider agent.Provider) *app.Workspace {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{CWD: filepath.Join(root, "project"), Selection: "test:model"}
	workspace, err := app.NewWorkspace(app.WorkspaceOptions{
		Config:          cfg,
		ProviderFactory: func(config.Config) agent.Provider { return provider },
		Repository:      session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func containsJSONKey(data []byte, key string) bool {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	return findJSONKey(value, key)
}

func findJSONKey(value any, key string) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value[key]; ok {
			return true
		}
		for _, child := range value {
			if findJSONKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if findJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}
