package openai

import (
	"context"
	"encoding/json"
	"errors"
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

func TestClientRetriesServerErrorThenSucceeds(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "temporary outage", http.StatusInternalServerError)
			return
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

	if msg.Content != "ok" || requests != 2 {
		t.Fatalf("unexpected retry result: content=%q requests=%d", msg.Content, requests)
	}
}

func TestClientRetriesRateLimitTwiceThenSucceeds(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= 2 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
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

	if msg.Content != "ok" || requests != 3 {
		t.Fatalf("unexpected retry result: content=%q requests=%d", msg.Content, requests)
	}
}

func TestClientRetriesTransportErrorThenSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, errors.New("temporary network error")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: httpClient})
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Content != "ok" || requests != 2 {
		t.Fatalf("unexpected retry result: content=%q requests=%d", msg.Content, requests)
	}
}

func TestClientDoesNotRetryClientError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	_, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if requests != 1 {
		t.Fatalf("client error should not be retried, requests=%d", requests)
	}
}

func TestClientStopsBackoffWhenContextCanceled(t *testing.T) {
	var cancel context.CancelFunc
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		cancel()
		http.Error(w, "temporary outage", http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancelFunc := context.WithCancel(context.Background())
	cancel = cancelFunc
	defer cancel()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	_, err := client.Complete(ctx, agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("canceled backoff should not retry, requests=%d", requests)
	}
}

func TestClientFallsBackFromUsageThenRetriesTransientError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch requests {
		case 1:
			if _, ok := payload["stream_options"]; !ok {
				t.Fatalf("first request should ask for usage: %+v", payload)
			}
			http.Error(w, "unsupported parameter: stream_options.include_usage", http.StatusBadRequest)
		case 2:
			if _, ok := payload["stream_options"]; ok {
				t.Fatalf("retry after fallback should omit stream_options: %+v", payload)
			}
			http.Error(w, "temporary outage", http.StatusInternalServerError)
		default:
			if _, ok := payload["stream_options"]; ok {
				t.Fatalf("transient retry should keep stream_options disabled: %+v", payload)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if msg.Content != "ok" || requests != 3 {
		t.Fatalf("unexpected fallback retry result: content=%q requests=%d", msg.Content, requests)
	}
}

func TestClientDoesNotRetryStreamingParseErrorAfterDelta(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {bad-json}\n\n"))
	}))
	defer server.Close()

	var streamed strings.Builder
	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	_, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, func(event agent.Event) {
		if event.Type == agent.EventTextDelta {
			streamed.WriteString(event.Text)
		}
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if streamed.String() != "partial" || requests != 1 {
		t.Fatalf("stream parse error should not retry after delta: streamed=%q requests=%d", streamed.String(), requests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
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
