package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

func TestNewForwardsCompatToClient(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["stream_options"]; ok {
			t.Fatalf("factory should forward compat: stream_options present: %+v", payload)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer factory-key" {
			t.Fatalf("factory should forward api key: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := New(config.Config{
		APIKey:  "factory-key",
		BaseURL: server.URL + "/v1",
		Model:   "factory-model",
		Compat:  config.ProviderCompat{NoStreamUsage: true},
	})
	msg, err := p.Complete(context.Background(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "ok" || requests != 1 {
		t.Fatalf("unexpected factory result: content=%q requests=%d", msg.Content, requests)
	}
}
