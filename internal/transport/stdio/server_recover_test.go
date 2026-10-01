package stdio

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/transport/jsonrpc"
)

// A panicking handler must not kill the server: the caller gets a -32603
// internal error carrying the request id, and the panic is logged with a
// stack trace.
func TestServeRequestRecoversPanic(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	var responses []jsonrpc.Response
	write := func(r jsonrpc.Response) { responses = append(responses, r) }
	panicking := func(context.Context, jsonrpc.Request) jsonrpc.Response {
		panic("handler bug")
	}
	serveRequest(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":7,"method":"run.start"}`),
		panicking, write, logger)

	if len(responses) != 1 {
		t.Fatalf("expected one response, got %d", len(responses))
	}
	resp := responses[0]
	if string(resp.ID) != "7" {
		t.Errorf("response must carry the request id, got %s", resp.ID)
	}
	if resp.Error == nil || resp.Error.Code != -32603 {
		t.Errorf("expected -32603 internal error, got %+v", resp.Error)
	}
	if !strings.Contains(logBuf.String(), "stdio request panicked") ||
		!strings.Contains(logBuf.String(), "handler bug") {
		t.Errorf("panic must be logged, got %q", logBuf.String())
	}
}

// serveRequest with a nil logger must not itself panic while reporting.
func TestServeRequestRecoversPanicWithoutLogger(t *testing.T) {
	var responses []jsonrpc.Response
	write := func(r jsonrpc.Response) { responses = append(responses, r) }
	panicking := func(context.Context, jsonrpc.Request) jsonrpc.Response {
		panic("boom")
	}
	serveRequest(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`),
		panicking, write, nil)
	if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != -32603 {
		t.Fatalf("expected one -32603 response, got %+v", responses)
	}
}

// Non-panic paths are unchanged: parse errors stay -32700, notifications
// (no id) produce no response.
func TestServeRequestNormalPaths(t *testing.T) {
	okHandle := func(context.Context, jsonrpc.Request) jsonrpc.Response {
		return jsonrpc.Response{JSONRPC: jsonrpc.Version, Result: "ok"}
	}
	var responses []jsonrpc.Response
	write := func(r jsonrpc.Response) { responses = append(responses, r) }

	serveRequest(context.Background(), []byte(`not json`), okHandle, write, nil)
	if len(responses) != 1 || responses[0].Error == nil || responses[0].Error.Code != -32700 {
		t.Fatalf("parse error must stay -32700, got %+v", responses)
	}

	responses = nil
	serveRequest(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notify"}`), okHandle, write, nil)
	if len(responses) != 0 {
		t.Fatalf("notification must produce no response, got %+v", responses)
	}
}

// A panicking notification (no id) must stay response-free: emitting an
// unsolicited -32603 would desynchronize the client.
func TestServeRequestPanickingNotificationStaysSilent(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	var responses []jsonrpc.Response
	write := func(r jsonrpc.Response) { responses = append(responses, r) }
	panicking := func(context.Context, jsonrpc.Request) jsonrpc.Response {
		panic("notification bug")
	}
	serveRequest(context.Background(),
		[]byte(`{"jsonrpc":"2.0","method":"notify"}`),
		panicking, write, logger)
	if len(responses) != 0 {
		t.Fatalf("panicking notification must produce no response, got %+v", responses)
	}
	if !strings.Contains(logBuf.String(), "stdio request panicked") {
		t.Errorf("panic must still be logged, got %q", logBuf.String())
	}
}
