package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testClient(t *testing.T, mux *http.ServeMux) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	return NewClient(Config{
		BaseURL: srv.URL, APIKey: "k", Dir: dir,
		HTTPClient: srv.Client(),
	}), srv
}

func TestGenerateImageB64(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	mux := http.NewServeMux()
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth = %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": png, "revised_prompt": "rev"}},
		})
	})
	c, _ := testClient(t, mux)
	results, err := c.GenerateImage(context.Background(), ImageRequest{Prompt: "a cat", N: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].RevisedPrompt != "rev" {
		t.Fatalf("results = %+v", results)
	}
	raw, err := os.ReadFile(results[0].Path)
	if err != nil || string(raw) != "fake-png" {
		t.Fatalf("file = %q, err = %v", raw, err)
	}
}

func TestGenerateImageEmptyPrompt(t *testing.T) {
	c, _ := testClient(t, http.NewServeMux())
	if _, err := c.GenerateImage(context.Background(), ImageRequest{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerateImageAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"bad prompt","type":"invalid_request"}}`))
	})
	c, _ := testClient(t, mux)
	_, err := c.GenerateImage(context.Background(), ImageRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "bad prompt") {
		t.Fatalf("err = %v", err)
	}
}

func TestSpeak(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		if p["input"] != "hello" {
			t.Errorf("input = %v", p["input"])
		}
		w.Write([]byte("fake-mp3"))
	})
	c, _ := testClient(t, mux)
	path, err := c.Speak(context.Background(), SpeakRequest{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".mp3") {
		t.Fatalf("path = %q", path)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "fake-mp3" {
		t.Fatalf("file = %q", raw)
	}
}

func TestTranscribe(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		json.NewEncoder(w).Encode(map[string]string{"text": "你好世界"})
	})
	c, _ := testClient(t, mux)
	audio := filepath.Join(t.TempDir(), "v.m4a")
	os.WriteFile(audio, []byte("fake-audio"), 0o600)
	text, err := c.Transcribe(context.Background(), TranscribeRequest{AudioPath: audio, Language: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "你好世界" {
		t.Fatalf("text = %q", text)
	}
}

func TestTranscribeMissingFile(t *testing.T) {
	c, _ := testClient(t, http.NewServeMux())
	if _, err := c.Transcribe(context.Background(), TranscribeRequest{AudioPath: "/nonexistent/x.mp3"}); err == nil {
		t.Fatal("expected error")
	}
}
