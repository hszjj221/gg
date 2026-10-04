package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/app"
)

func testBot(t *testing.T, allow []int64) *Bot {
	t.Helper()
	home := t.TempDir()
	b, err := New(Config{
		Token:      "test-token",
		AllowChats: allow,
		Runtime:    &app.Runtime{},
		HomeDir:    home,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNewValidation(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"empty token", Config{Runtime: &app.Runtime{}, HomeDir: home}},
		{"nil rt", Config{Token: "x", HomeDir: home}},
		{"empty home", Config{Token: "x", Runtime: &app.Runtime{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestAllowlist(t *testing.T) {
	b := testBot(t, []int64{123})
	if !b.allowed(123) {
		t.Fatal("123 should be allowed")
	}
	if b.allowed(456) {
		t.Fatal("456 should not be allowed")
	}
	// Empty allowlist means nobody.
	b2 := testBot(t, nil)
	if b2.allowed(123) {
		t.Fatal("empty allowlist should allow nobody")
	}
}

func TestOffsetPersistence(t *testing.T) {
	home := t.TempDir()
	mk := func() *Bot {
		b, err := New(Config{
			Token:      "x",
			AllowChats: []int64{1},
			Runtime:    &app.Runtime{},
			HomeDir:    home,
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := mk()
	if b.currentOffset() != 0 {
		t.Fatalf("initial offset = %d, want 0", b.currentOffset())
	}
	b.saveOffset(42)
	if b.currentOffset() != 42 {
		t.Fatalf("offset = %d, want 42", b.currentOffset())
	}
	// A new Bot on the same home dir must resume from the saved offset.
	b2 := mk()
	if b2.currentOffset() != 42 {
		t.Fatalf("reloaded offset = %d, want 42", b2.currentOffset())
	}
	fi, err := os.Stat(filepath.Join(home, ".gg", "telegram", "offset"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("offset file mode %o too open", fi.Mode().Perm())
	}
}

func TestSplitMessage(t *testing.T) {
	if got := splitMessage("abc", 4096); len(got) != 1 || got[0] != "abc" {
		t.Fatalf("short message split wrong: %q", got)
	}
	long := strings.Repeat("x", 9000)
	chunks := splitMessage(long, 4096)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len([]rune(chunks[0])) != 4096 || len([]rune(chunks[2])) != 808 {
		t.Fatalf("chunk sizes wrong: %d %d", len(chunks[0]), len(chunks[2]))
	}
}

func TestGetUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.FormValue("offset") != "10" {
			t.Errorf("offset = %q, want 10", r.FormValue("offset"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": []map[string]any{
				{"update_id": 10, "message": map[string]any{
					"message_id": 1,
					"chat":       map[string]any{"id": 123, "type": "private"},
					"text":       "hello",
				}},
				{"update_id": 11, "message": map[string]any{
					"message_id": 2,
					"chat":       map[string]any{"id": 123, "type": "private"},
					"voice":      map[string]any{"file_id": "abc", "duration": 3},
				}},
			},
		})
	}))
	defer srv.Close()

	api := newAPIForURL("tok", srv.URL)
	updates, err := api.GetUpdates(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 {
		t.Fatalf("got %d updates", len(updates))
	}
	if updates[0].Message.Text != "hello" || updates[0].Message.Chat.ID != 123 {
		t.Fatalf("update 0 decoded wrong: %+v", updates[0].Message)
	}
	if updates[1].Message.Voice == nil || updates[1].Message.Voice.FileID != "abc" {
		t.Fatalf("update 1 voice decoded wrong: %+v", updates[1].Message)
	}
}

func TestRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"error_code":  429,
			"description": "Too Many Requests",
			"parameters":  map[string]any{"retry_after": 5},
		})
	}))
	defer srv.Close()

	api := newAPIForURL("tok", srv.URL)
	_, err := api.GetUpdates(context.Background(), 0, 0)
	rl, ok := err.(*RateLimitedError)
	if !ok {
		t.Fatalf("expected RateLimitedError, got %v", err)
	}
	if rl.RetryAfter != 5e9 {
		t.Fatalf("retry after = %v", rl.RetryAfter)
	}
}

func TestSendMessageChunks(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.FormValue("text"))
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer srv.Close()

	api := newAPIForURL("tok", srv.URL)
	if err := api.SendMessage(context.Background(), 123, strings.Repeat("y", 5000)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if len([]rune(got[0])) != 4096 || len([]rune(got[1])) != 904 {
		t.Fatalf("chunk sizes wrong")
	}
}

func TestGetFileAndDownload(t *testing.T) {
	const payload = "fake-ogg-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"result": map[string]any{"file_path": "voice/file_1.ogg"},
			})
		case strings.Contains(r.URL.Path, "/file/"):
			w.Write([]byte(payload))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	api := newAPIForURL("tok", srv.URL)
	ctx := context.Background()
	fp, err := api.GetFilePath(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if fp != "voice/file_1.ogg" {
		t.Fatalf("file_path = %q", fp)
	}
	dst := filepath.Join(t.TempDir(), "v.ogg")
	if err := api.DownloadFile(ctx, fp, dst); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != payload {
		t.Fatalf("downloaded %q", data)
	}
}

func TestStderrInjection(t *testing.T) {
	home := t.TempDir()
	var buf strings.Builder
	b, err := New(Config{
		Token:   "test-token",
		Runtime: &app.Runtime{},
		HomeDir: home,
		Stderr:  &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.stderr != io.Writer(&buf) {
		t.Fatal("injected stderr writer was not kept")
	}
	// Nil defaults to os.Stderr (back-compat).
	b2, err := New(Config{
		Token:   "test-token",
		Runtime: &app.Runtime{},
		HomeDir: home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b2.stderr == nil {
		t.Fatal("nil Stderr must default to os.Stderr")
	}
}
