package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestClientOmitsStreamUsageWhenCompatSet(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["stream_options"]; ok {
			t.Fatalf("compat noStreamUsage should omit stream_options: %+v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		Compat:     Compat{NoStreamUsage: true},
		HTTPClient: server.Client(),
	})
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "ok" || requests != 1 {
		t.Fatalf("compat should skip the usage probe: content=%q requests=%d", msg.Content, requests)
	}
}

func TestClientUsesCompletionTokensWhenCompatSet(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["max_tokens"]; ok {
			t.Fatalf("compat completionTokens should not send max_tokens: %+v", payload)
		}
		if payload["max_completion_tokens"] != float64(100) {
			t.Fatalf("compat completionTokens should send max_completion_tokens=100: %+v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:     "test-key",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		Compat:     Compat{CompletionTokens: true},
		HTTPClient: server.Client(),
	})
	req := agent.Request{
		Messages:        []agent.Message{{Role: agent.RoleUser, Content: "hi"}},
		MaxOutputTokens: 100,
	}
	msg, err := client.Complete(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "ok" || requests != 1 {
		t.Fatalf("compat should skip the max_tokens probe: content=%q requests=%d", msg.Content, requests)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("120"); got != 120*time.Second {
		t.Errorf("delta-seconds: got %v", got)
	}
	if got := parseRetryAfter("  30 "); got != 30*time.Second {
		t.Errorf("padded delta-seconds: got %v", got)
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got < 85*time.Second || got > 90*time.Second {
		t.Errorf("http-date: got %v", got)
	}
	for _, v := range []string{"", "soon", "-5", "0"} {
		if got := parseRetryAfter(v); got != 0 {
			t.Errorf("parseRetryAfter(%q) = %v, want 0", v, got)
		}
	}
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("past http-date: got %v", got)
	}
}

func TestRetryDelayBounds(t *testing.T) {
	plain := errors.New("boom")
	for i := 0; i < 200; i++ {
		if d := retryDelay(0, plain); d < 0 || d > retryBaseDelay {
			t.Fatalf("attempt 0: delay %v out of [0, %v]", d, retryBaseDelay)
		}
		if d := retryDelay(1, plain); d < 0 || d > 2*retryBaseDelay {
			t.Fatalf("attempt 1: delay %v out of [0, %v]", d, 2*retryBaseDelay)
		}
	}
	// Overflow-safe: huge attempt still caps at retryMaxDelay.
	for i := 0; i < 50; i++ {
		if d := retryDelay(100, plain); d < 0 || d > retryMaxDelay {
			t.Fatalf("attempt 100: delay %v out of [0, %v]", d, retryMaxDelay)
		}
	}
}

func TestRetryDelayHonorsRetryAfter(t *testing.T) {
	err := apiError{statusCode: http.StatusTooManyRequests, retryAfter: 2 * time.Second}
	if d := retryDelay(0, err); d != 2*time.Second {
		t.Errorf("server Retry-After must win: got %v", d)
	}
	err.retryAfter = time.Hour
	if d := retryDelay(0, err); d != maxRetryAfter {
		t.Errorf("Retry-After must be capped at %v: got %v", maxRetryAfter, d)
	}
	// Absent header falls back to backoff.
	err.retryAfter = 0
	if d := retryDelay(0, err); d < 0 || d > retryBaseDelay {
		t.Errorf("absent Retry-After should use backoff: got %v", d)
	}
}

func TestClientHonorsRetryAfterHeader(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "gpt-test", HTTPClient: server.Client()})
	start := time.Now()
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "ok" || requests != 2 {
		t.Fatalf("unexpected result: content=%q requests=%d", msg.Content, requests)
	}
	// The server asked for 1s; jittered backoff alone would usually be far less.
	if elapsed < 900*time.Millisecond {
		t.Errorf("Retry-After: 1 was not honored, elapsed %v", elapsed)
	}
}

func TestClientEmitsThinkingDeltaFromReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"let me \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "reasoner", HTTPClient: server.Client()})
	var events []agent.Event
	msg, err := client.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, func(e agent.Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "done" {
		t.Fatalf("thinking must not leak into content: %q", msg.Content)
	}
	var thinking, text []string
	for _, e := range events {
		switch e.Type {
		case agent.EventThinkingDelta:
			thinking = append(thinking, e.Text)
		case agent.EventTextDelta:
			text = append(text, e.Text)
		default:
			t.Fatalf("unexpected event type: %q", e.Type)
		}
	}
	if got := strings.Join(thinking, ""); got != "let me think" {
		t.Fatalf("thinking deltas not normalized: %q", got)
	}
	if got := strings.Join(text, ""); got != "done" {
		t.Fatalf("text deltas broken: %q", got)
	}
}

func TestClientEchoesReasoningContentForToolContinuation(t *testing.T) {
	var mu sync.Mutex
	var secondBody []byte
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n == 1 {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"let me call the tool\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		secondBody = body
		mu.Unlock()
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "reasoner", HTTPClient: server.Client()})
	ctx := context.Background()
	reply, err := client.Complete(ctx, agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.ToolCalls) != 1 {
		t.Fatalf("expected one tool call: %+v", reply)
	}
	if reply.Reasoning != "let me call the tool" {
		t.Fatalf("reasoning not retained on assistant message: %q", reply.Reasoning)
	}

	history := []agent.Message{
		{Role: agent.RoleUser, Content: "hi"},
		reply.Message,
		{Role: agent.RoleTool, Content: "tool output", ToolCallID: "call_1", ToolName: "read"},
	}
	if _, err := client.Complete(ctx, agent.Request{Messages: history}, nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(secondBody, &payload); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range payload.Messages {
		if m["role"] != "assistant" {
			continue
		}
		if _, ok := m["tool_calls"]; !ok {
			continue
		}
		rc, _ := m["reasoning_content"].(string)
		if rc != "let me call the tool" {
			t.Fatalf("tool continuation missing reasoning_content: %v", m)
		}
		found = true
	}
	if !found {
		t.Fatal("assistant tool-call message not found in second request")
	}
}
