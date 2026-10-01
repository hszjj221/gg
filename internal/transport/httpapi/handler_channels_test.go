package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /health reports channel states when a status callback is installed, and
// omits the field otherwise.
func TestHealthReportsChannelStatus(t *testing.T) {
	handler := testHandler(t, "secret")
	handler.SetChannelStatus(func() []ChannelStatus {
		return []ChannelStatus{
			{Name: "scheduler", State: "running"},
			{Name: "telegram", State: "failed", Error: "unauthorized", FailedAt: "2026-10-01T00:00:00+08:00"},
		}
	})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health: status=%d", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		`"channels"`,
		`"name":"scheduler"`, `"state":"running"`,
		`"name":"telegram"`, `"state":"failed"`,
		`"error":"unauthorized"`, `"failedAt":"2026-10-01T00:00:00+08:00"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("health body missing %s: %s", want, body)
		}
	}
}

func TestHealthOmitsChannelsWithoutCallback(t *testing.T) {
	handler := testHandler(t, "secret")
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health: status=%d", response.Code)
	}
	if strings.Contains(response.Body.String(), `"channels"`) {
		t.Errorf("channels must be omitted without a callback: %s", response.Body.String())
	}
}
