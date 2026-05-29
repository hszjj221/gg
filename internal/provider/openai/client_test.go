package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestClientAggregatesStreamingText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("missing auth header: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Content != "hello" {
		t.Fatalf("unexpected content: %q", msg.Content)
	}
}

func TestClientAggregatesStreamingToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read","arguments":"{\"pa"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}
		_, _ = w.Write([]byte(strings.Join(chunks, "\n\n") + "\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	msg, err := client.Complete(context.Background(), agent.Request{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: "read"}},
		Tools:    []agent.ToolDefinition{{Name: "read", Description: "read", Parameters: map[string]any{"type": "object"}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.StopReason != agent.StopReasonToolUse || len(msg.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", msg)
	}
	if got := string(msg.ToolCalls[0].Arguments); got != `{"path":"README.md"}` {
		t.Fatalf("unexpected args: %s", got)
	}
}

func TestClientRequestsAndParsesStreamingUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		streamOptions, ok := payload["stream_options"].(map[string]any)
		if !ok || streamOptions["include_usage"] != true {
			t.Fatalf("missing stream_options.include_usage: %+v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Usage.PromptTokens != 7 || msg.Usage.CompletionTokens != 3 || msg.Usage.TotalTokens != 10 {
		t.Fatalf("unexpected usage: %+v", msg.Usage)
	}
}

func TestClientFallsBackWhenStreamingUsageIsUnsupported(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			if _, ok := payload["stream_options"]; !ok {
				t.Fatalf("first request should ask for usage: %+v", payload)
			}
			http.Error(w, "unsupported parameter: stream_options.include_usage", http.StatusBadRequest)
			return
		}
		if _, ok := payload["stream_options"]; ok {
			t.Fatalf("fallback request should omit stream_options: %+v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Content != "ok" || !msg.Usage.IsZero() || requests != 2 {
		t.Fatalf("unexpected fallback result: content=%q usage=%+v requests=%d", msg.Content, msg.Usage, requests)
	}
}
