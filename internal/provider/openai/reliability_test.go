package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestRejectPrematureStreamEOF(t *testing.T) {
	msg, err := parseStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"), nil)
	if err == nil {
		t.Fatalf("incomplete stream accepted as %q, content=%q", msg.StopReason, msg.Content)
	}
}

func TestOutputLimitCompatibilityComposesWithUsageFallback(t *testing.T) {
	calls := 0
	client := NewClient(Config{APIKey: "test", BaseURL: "http://provider.test/v1", Model: "test", HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		status, body := http.StatusBadRequest, ""
		switch calls {
		case 1:
			if payload["max_tokens"] != float64(123) {
				t.Fatal("missing initial output limit")
			}
			body = "Unsupported parameter max_tokens; use max_completion_tokens instead"
		case 2:
			if payload["max_completion_tokens"] != float64(123) || payload["max_tokens"] != nil {
				t.Fatal("output limit fallback failed")
			}
			body = "Unsupported parameter stream_options.include_usage"
		case 3:
			if payload["max_completion_tokens"] != float64(123) || payload["stream_options"] != nil {
				t.Fatal("fallback settings were lost")
			}
			status = http.StatusOK
			body = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"
		default:
			t.Fatal("unexpected retry")
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}})
	result, err := client.Complete(context.Background(), agent.Request{MaxOutputTokens: 123}, nil)
	if err != nil || result.Content != "ok" || calls != 3 {
		t.Fatalf("compatibility retry failed: calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestIncompleteToolStreamsNeverReturnExecutableCalls(t *testing.T) {
	cases := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"write","arguments":"{\"path\":"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"write","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","function":{"name":"write","arguments":"{}"}}]},"finish_reason":"length"}]}`,
		`{"error":{"message":"provider failed"}}`,
	}
	for _, data := range cases {
		msg, err := parseStream(strings.NewReader("data: "+data+"\n\ndata: [DONE]\n\n"), nil)
		if err == nil || len(msg.ToolCalls) != 0 {
			t.Fatalf("invalid stream exposed calls: data=%s msg=%+v err=%v", data, msg, err)
		}
	}
}

func TestLengthLimitPreservesPartialTextAndStatus(t *testing.T) {
	msg, err := parseStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\n"), nil)
	if err == nil || msg.Content != "partial" || msg.StopReason != agent.StopReasonMaxTokens {
		t.Fatalf("lost partial response: %+v err=%v", msg, err)
	}
}

func TestRequestIncludesOutputBudgetAndIncompleteResponseContext(t *testing.T) {
	client := NewClient(Config{Model: "test"})
	payload := client.requestPayload(agent.Request{MaxOutputTokens: 123, Messages: []agent.Message{{Role: agent.RoleAssistant, Content: "partial", Error: "connection lost"}}}, true)
	if payload["max_tokens"] != 123 {
		t.Fatal("missing output budget")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Response incomplete: connection lost") {
		t.Fatal("next request cannot see that the response was incomplete")
	}
}
