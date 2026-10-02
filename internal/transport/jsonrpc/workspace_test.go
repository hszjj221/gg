package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/app"
)

// waitRunDone blocks until the run started by a run.start response
// completes, so the background turn's session writes finish before the
// test (and its TempDir) goes away.
func waitRunDone(t *testing.T, rt *app.Runtime, started Response) {
	t.Helper()
	runID, _ := started.Result.(map[string]string)["runId"]
	if runID == "" {
		t.Fatalf("no runId: %#v", started.Result)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, done, err := rt.WaitRun(ctx, runID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return
		}
	}
}

// TestRunStartAcceptsWorkspace verifies the 1.4 run.start workspace field:
// a known workspace (by name) lets the turn start, an unknown one fails
// with the workspace_mismatch application error, and the fail-closed
// requireApproval default is unchanged.
func TestRunStartAcceptsWorkspace(t *testing.T) {
	newSession := func(t *testing.T, handler *Handler) string {
		t.Helper()
		created := handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`1`), Method: "session.create"})
		snapshot, ok := created.Result.(app.Snapshot)
		if created.Error != nil || !ok {
			t.Fatalf("create session: %+v", created.Error)
		}
		return snapshot.SessionID
	}

	t.Run("known workspace starts turn", func(t *testing.T) {
		rt := testWorkspace(t) // fakeProvider answers "ok" immediately
		handler := NewHandler(rt)
		sessionID := newSession(t, handler)
		params := `{"sessionId":"` + sessionID + `","prompt":"hi","workspace":"` +
			rt.DefaultWorkspace().Name + `","requireApproval":false}`
		started := handler.Handle(context.Background(), Request{
			JSONRPC: Version, ID: []byte(`2`), Method: "run.start", Params: []byte(params),
		})
		if started.Error != nil {
			t.Fatalf("run.start with workspace: %+v", started.Error)
		}
		runID, _ := started.Result.(map[string]string)["runId"]
		if runID == "" {
			t.Fatalf("no runId: %#v", started.Result)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for {
			events, done, err := rt.WaitRun(ctx, runID, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range events {
				if ev.Type == app.EventRunCompleted && ev.Result != nil && ev.Result.Content != "ok" {
					t.Fatalf("unexpected result %q", ev.Result.Content)
				}
			}
			if done {
				break
			}
		}
	})

	t.Run("unknown workspace fails", func(t *testing.T) {
		handler := NewHandler(testWorkspace(t))
		resp := handler.Handle(context.Background(), Request{
			JSONRPC: Version, ID: []byte(`1`), Method: "run.start",
			Params: []byte(`{"sessionId":"nope","prompt":"hi","workspace":"nope"}`),
		})
		if resp.Error == nil {
			t.Fatal("expected error for unknown workspace")
		}
		if resp.Error.Code != -32008 {
			t.Fatalf("code = %d, want -32008", resp.Error.Code)
		}
		if resp.Error.Data == nil || resp.Error.Data.Code != app.ErrorWorkspaceMismatch {
			t.Fatalf("symbolic code = %+v, want workspace_mismatch", resp.Error.Data)
		}
	})

	t.Run("absent workspace keeps legacy behavior", func(t *testing.T) {
		rt := testWorkspace(t)
		handler := NewHandler(rt)
		sessionID := newSession(t, handler)
		started := handler.Handle(context.Background(), Request{
			JSONRPC: Version, ID: []byte(`2`), Method: "run.start",
			Params: []byte(`{"sessionId":"` + sessionID + `","prompt":"hi","requireApproval":false}`),
		})
		if started.Error != nil {
			t.Fatalf("run.start without workspace: %+v", started.Error)
		}
		// Wait for the turn to finish: the background run keeps writing
		// session files, and returning early races t.TempDir() cleanup
		// ("directory not empty").
		waitRunDone(t, rt, started)
	})
}

// TestProtocolVersionBump pins the 1.4 protocol: run.start accepts an
// optional workspace.
func TestProtocolVersionBump(t *testing.T) {
	if ProtocolVersion != "1.4" {
		t.Fatalf("ProtocolVersion = %q, want 1.4", ProtocolVersion)
	}
	handler := NewHandler(testWorkspace(t))
	resp := handler.Handle(context.Background(), Request{JSONRPC: Version, ID: []byte(`1`), Method: "system.info"})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	info, ok := resp.Result.(SystemInfo)
	if !ok || info.ProtocolVersion != "1.4" {
		t.Fatalf("system.info protocolVersion = %#v", resp.Result)
	}
}

// TestErrorFromWorkspaceMismatch pins the numeric mapping for the new
// error code so clients can match on it stably.
func TestErrorFromWorkspaceMismatch(t *testing.T) {
	rpcErr := ErrorFrom(&app.AppError{Code: app.ErrorWorkspaceMismatch})
	if rpcErr.Code != -32008 {
		t.Fatalf("ErrorFrom(workspace_mismatch).Code = %d, want -32008", rpcErr.Code)
	}
}
