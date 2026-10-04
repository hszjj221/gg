package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/provider/openai"
	"github.com/hszjj221/gg/internal/runlog"
	"github.com/hszjj221/gg/internal/session"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

type diagnosticProviderFunc func(context.Context, agent.Request, func(agent.Event)) (agent.AssistantMessage, error)

func (f diagnosticProviderFunc) Complete(ctx context.Context, req agent.Request, events func(agent.Event)) (agent.AssistantMessage, error) {
	return f(ctx, req, events)
}

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type diagnosticTool struct{ executed atomic.Int64 }

func (t *diagnosticTool) Name() string { return "diagnostic" }
func (t *diagnosticTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{Name: t.Name(), Parameters: map[string]any{"type": "object"}}
}
func (t *diagnosticTool) ApprovalRequest(args json.RawMessage) (agent.ApprovalRequest, error) {
	return agent.ApprovalRequest{ToolName: t.Name(), Summary: "private approval", Arguments: args}, nil
}
func (t *diagnosticTool) Execute(context.Context, json.RawMessage) agent.ToolResult {
	t.executed.Add(1)
	return agent.ToolResult{Content: []agent.ContentBlock{{Type: agent.ContentText, Text: "private tool result"}}, Usage: agent.Usage{PromptTokens: 2, TotalTokens: 2}}
}

type diagnosticApprover bool

func (a diagnosticApprover) Approve(context.Context, agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return agent.ApprovalDecision{Allow: bool(a)}, nil
}

func diagnosticRecords(t *testing.T, data string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func TestRunLogsCorrelateRequestsRetriesAndToolExecution(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var calls atomic.Int64
	client := openai.NewClient(openai.Config{APIKey: "private API key", BaseURL: "https://provider.invalid", Model: "model", HTTPClient: &http.Client{Transport: diagnosticTransport(func(*http.Request) (*http.Response, error) {
		status, body := http.StatusOK, ""
		switch calls.Add(1) {
		case 1:
			status, body = 503, "private provider failure"
		case 2:
			status, body = 400, "unsupported stream_options.include_usage private compatibility response"
		case 3:
			body = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"diagnostic","arguments":"{\"text\":\"private argument\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}` + "\n\ndata: [DONE]\n\n"
		default:
			body = `data: {"choices":[{"delta":{"content":"private assistant response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}` + "\n\ndata: [DONE]\n\n"
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	tool := &diagnosticTool{}
	svc := NewService(Options{Config: config.Config{CWD: dir, Provider: "test", Model: "model", Selection: "test:model"}, Store: store, Log: logger, ProviderFactory: func(config.Config) agent.Provider { return client }, ToolProviders: []ToolProvider{{Build: func(context.Context, ToolContext) ([]agent.Tool, error) { return []agent.Tool{tool}, nil }}}})
	manager := NewManagerWithOptions(ManagerOptions{Log: logger})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = manager.Close(ctx) })
	id, err := manager.Add(svc)
	if err != nil {
		t.Fatal(err)
	}
	run, err := manager.StartTurnWithApprover(ctx, id, "private prompt", diagnosticApprover(true))
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Await(ctx)
	if err != nil || result.Usage.TotalTokens != 17 || tool.executed.Load() != 1 || calls.Load() != 4 {
		t.Fatalf("execution changed: %+v, %v, calls=%d", result, err, calls.Load())
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private") {
		t.Fatalf("diagnostics leaked request/response content: %s", output.String())
	}
	seen := map[string]int{}
	for _, record := range diagnosticRecords(t, output.String()) {
		message := record["msg"].(string)
		seen[message]++
		if record["sessionID"] != id || record["runID"] != run.ID() {
			t.Fatalf("uncorrelated record: %+v", record)
		}
		switch message {
		case "provider retry scheduled":
			if record["requestID"] != float64(1) || record["httpStatus"] != float64(503) {
				t.Fatalf("retry lost cause: %+v", record)
			}
		case "provider attempts finished":
			if record["requestID"] == float64(1) && (record["attempts"] != float64(3) || record["retries"] != float64(1) || record["compatibilityRetries"] != float64(1)) {
				t.Fatalf("wrong attempt counts: %+v", record)
			}
		case "tool execution finished":
			if record["toolCallID"] != "call-1" || record["outcome"] != "success" {
				t.Fatalf("tool outcome lost: %+v", record)
			}
		case "run finished":
			if record["totalTokens"] != float64(17) || record["outcome"] != "success" {
				t.Fatalf("run outcome lost: %+v", record)
			}
		}
	}
	for message, count := range map[string]int{"run started": 1, "run finished": 1, "model request finished": 2, "provider retry scheduled": 1, "provider compatibility fallback": 1, "tool approval finished": 1, "tool execution finished": 1} {
		if seen[message] != count {
			t.Fatalf("%s count = %d, want %d", message, seen[message], count)
		}
	}
}

func TestConcurrentRunLogsKeepSessionIdentity(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	manager := NewManagerWithOptions(ManagerOptions{Log: logger})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = manager.Close(ctx) })
	runs := map[string]string{}
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		store, err := session.NewStore(filepath.Join(dir, "session.jsonl"), dir)
		if err != nil {
			t.Fatal(err)
		}
		svc := NewService(Options{Config: config.Config{CWD: dir, Selection: "test:model"}, Store: store, Log: logger, ToolProviders: []ToolProvider{}, ProviderFactory: func(config.Config) agent.Provider { return &runtimeProvider{block: true} }})
		id, err := manager.Add(svc)
		if err != nil {
			t.Fatal(err)
		}
		run, err := manager.StartTurn(ctx, id, "private prompt", false)
		if err != nil {
			t.Fatal(err)
		}
		runs[run.ID()] = id
	}
	for id := range runs {
		if err := manager.Cancel(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
	finished := 0
	for _, record := range diagnosticRecords(t, output.String()) {
		runID, _ := record["runID"].(string)
		if record["sessionID"] != runs[runID] || runID == "" {
			t.Fatalf("cross-session log: %+v", record)
		}
		if record["msg"] == "run finished" {
			finished++
			if record["outcome"] != "canceled" {
				t.Fatalf("wrong cancellation: %+v", record)
			}
		}
	}
	if finished != 2 {
		t.Fatalf("finished %d runs", finished)
	}
}

func TestCompactionLogsHaveSeparateRequestKind(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(Options{Log: logger, Config: config.Config{Selection: "test:model", Context: config.ContextConfig{TailTurns: 1}}, History: []agent.Message{{Role: agent.RoleUser, Content: "one"}, {Role: agent.RoleAssistant, Content: "two"}, {Role: agent.RoleUser, Content: "three"}}, ToolProviders: []ToolProvider{}, ProviderFactory: func(config.Config) agent.Provider { return &runtimeProvider{} }})
	defer svc.Close()
	if _, err := svc.Run(context.Background(), "/compact", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, record := range diagnosticRecords(t, output.String()) {
		if record["msg"] == "model request finished" {
			if record["requestKind"] != "compaction" {
				t.Fatalf("kind = %v", record)
			}
			return
		}
	}
	t.Fatalf("compaction request missing: %s", output.String())
}

func TestSharedProviderRequestsKeepUniqueDiagnosticIDs(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})).With("sessionID", "session", "runID", "run")
	ctx := runlog.WithLogger(context.Background(), logger)
	provider := &observedProvider{model: "test:model", Provider: diagnosticProviderFunc(func(ctx context.Context, _ agent.Request, _ func(agent.Event)) (agent.AssistantMessage, error) {
		runlog.Logger(ctx, nil).DebugContext(ctx, "nested request trace")
		return agent.AssistantMessage{}, nil
	})}
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); _, _ = provider.Complete(ctx, agent.Request{}, nil) }()
	}
	workers.Wait()
	seen := map[float64]bool{}
	for _, record := range diagnosticRecords(t, output.String()) {
		if record["sessionID"] != "session" || record["runID"] != "run" {
			t.Fatalf("lost context: %+v", record)
		}
		if record["msg"] != "nested request trace" {
			continue
		}
		id := record["requestID"].(float64)
		if id < 1 || id > 16 || seen[id] {
			t.Fatalf("duplicate/missing request ID: %+v", record)
		}
		seen[id] = true
	}
	if len(seen) != 16 {
		t.Fatalf("only %d shared requests logged", len(seen))
	}
}
