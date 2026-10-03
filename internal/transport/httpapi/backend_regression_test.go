package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRPCRejectsTrailingDataAndOversizeBeforeExecution(t *testing.T) {
	h := testHandler(t, "secret")
	head := `{"jsonrpc":"2.0","id":1,"method":"session.create","params":{"name":"should not exist"}}`
	for _, tc := range []struct {
		tail   string
		status int
	}{
		{`{"garbage":true}`, 400}, {`invalid`, 400}, {strings.Repeat(" ", maxRequestBytes), 413},
	} {
		req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(head+tc.tail))
		req.Header.Set("Authorization", "Bearer secret")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Fatalf("status=%d want=%d", res.Code, tc.status)
		}
	}
	sessions, err := h.rt.ListSessions()
	if err != nil || len(sessions) != 0 {
		t.Fatalf("invalid request executed: %v %v", sessions, err)
	}
}

func TestRPCAllowsExactlyBoundedValidBody(t *testing.T) {
	h := testHandler(t, "secret")
	body := `{"jsonrpc":"2.0","id":1,"method":"system.info"}`
	body += strings.Repeat(" ", maxRequestBytes-len(body))
	req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("boundary status=%d", res.Code)
	}
}
