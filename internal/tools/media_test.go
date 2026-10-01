package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/media"
)

func testMediaClient(t *testing.T, mux *http.ServeMux) *media.Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return media.NewClient(media.Config{
		BaseURL: srv.URL, APIKey: "k", Dir: t.TempDir(), HTTPClient: srv.Client(),
	})
}

func TestImageGenerateTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": "ZmFrZS1wbmc="}},
		})
	})
	tool := NewImageGenerateTool(testMediaClient(t, mux))
	res := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"a cat"}`))
	if res.IsError {
		t.Fatalf("error: %v", res.Content)
	}
	if !strings.HasPrefix(res.Content[0].Text, "saved: ") {
		t.Fatalf("result = %q", res.Content[0].Text)
	}
	// Approval is required: image gen costs money.
	if _, err := tool.ApprovalRequest(json.RawMessage(`{"prompt":"a cat"}`)); err != nil {
		t.Fatalf("approval: %v", err)
	}
}

func TestTTSTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-mp3"))
	})
	tool := NewTTSTool(testMediaClient(t, mux))
	res := tool.Execute(context.Background(), json.RawMessage(`{"text":"hi"}`))
	if res.IsError {
		t.Fatalf("error: %v", res.Content)
	}
	// Approval is required: TTS is a paid external API call plus a file write.
	req, err := tool.ApprovalRequest(json.RawMessage(`{"text":"hello world","voice":"v","format":"wav"}`))
	if err != nil {
		t.Fatalf("approval: %v", err)
	}
	if req.ToolName != "tts" || !strings.Contains(req.Summary, "hello world") {
		t.Fatalf("unexpected approval request: %+v", req)
	}
	// The approval must show the effective voice: an empty voice is
	// substituted with media.DefaultVoice ("alloy") before the paid
	// request is made, so showing "default" would mislead the approver.
	req, err = tool.ApprovalRequest(json.RawMessage(`{"text":"hello"}`))
	if err != nil {
		t.Fatalf("approval: %v", err)
	}
	if !strings.Contains(req.Summary, "alloy") || !strings.Contains(req.Details, "voice: alloy") {
		t.Fatalf("approval request must show effective voice alloy: %+v", req)
	}
	if _, err := tool.ApprovalRequest(json.RawMessage(`{bad json`)); err == nil {
		t.Fatal("expected error for invalid arguments")
	}
}

func TestSTTTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"text": "hello"})
	})
	tool := NewSTTTool(testMediaClient(t, mux))
	audio := filepath.Join(t.TempDir(), "v.mp3")
	os.WriteFile(audio, []byte("x"), 0o600)
	raw, _ := json.Marshal(map[string]string{"audio_path": audio})
	res := tool.Execute(context.Background(), raw)
	if res.IsError {
		t.Fatalf("error: %v", res.Content)
	}
	if res.Content[0].Text != "hello" {
		t.Fatalf("result = %q", res.Content[0].Text)
	}
	// Missing file is an error, not a provider call.
	res = tool.Execute(context.Background(), json.RawMessage(`{"audio_path":"/nope.mp3"}`))
	if !res.IsError {
		t.Fatal("expected error for missing file")
	}
	// Approval is required: STT uploads a local file to a paid external
	// transcription API.
	req, err := tool.ApprovalRequest(raw)
	if err != nil {
		t.Fatalf("approval: %v", err)
	}
	if req.ToolName != "stt" || !strings.Contains(req.Details, "audio_path: "+audio) {
		t.Fatalf("unexpected approval request: %+v", req)
	}
	if !strings.Contains(req.Details, "language: auto") {
		t.Fatalf("approval request must show effective language auto: %+v", req)
	}
	if _, err := tool.ApprovalRequest(json.RawMessage(`{bad json`)); err == nil {
		t.Fatal("expected error for invalid arguments")
	}
}

func TestSTTToolRoots(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"text": "hello"})
	})
	root := t.TempDir()
	audio := filepath.Join(root, "v.mp3")
	os.WriteFile(audio, []byte("x"), 0o600)
	outside := filepath.Join(t.TempDir(), "secret.mp3")
	os.WriteFile(outside, []byte("x"), 0o600)

	tool := NewSTTToolWithRoots(testMediaClient(t, mux), []string{root})

	// Inside root: allowed.
	raw, _ := json.Marshal(map[string]string{"audio_path": audio})
	if res := tool.Execute(context.Background(), raw); res.IsError {
		t.Fatalf("inside root: unexpected error: %v", res.Content)
	}
	// Absolute path outside roots: rejected without provider call.
	raw, _ = json.Marshal(map[string]string{"audio_path": outside})
	if res := tool.Execute(context.Background(), raw); !res.IsError {
		t.Fatal("outside root: expected error")
	}
	// Symlink inside root pointing outside: rejected.
	link := filepath.Join(root, "link.mp3")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlink not supported")
	}
	raw, _ = json.Marshal(map[string]string{"audio_path": link})
	if res := tool.Execute(context.Background(), raw); !res.IsError {
		t.Fatal("symlink escape: expected error")
	}
	// Path traversal: rejected.
	raw, _ = json.Marshal(map[string]string{"audio_path": "../secret.mp3"})
	if res := tool.Execute(context.Background(), raw); !res.IsError {
		t.Fatal("traversal: expected error")
	}
}
