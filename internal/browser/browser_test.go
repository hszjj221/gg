package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/browser/ws"
)

// fakeCDPServer answers CDP methods with canned results.
func startFakeCDP(t *testing.T) *ws.Conn {
	t.Helper()
	ln := startFakeWSServer(t)
	c, err := ws.Dial("ws://"+ln+"/cdp", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSessionNavigateAndText(t *testing.T) {
	c := startFakeCDP(t)
	s := &Session{ws: c, pending: map[int64]chan cdpResult{}}
	defer s.Close()
	go s.readLoop()

	ctx := context.Background()
	if err := s.Navigate(ctx, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	text, err := s.Text(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Example Domain" {
		t.Fatalf("text = %q", text)
	}
	title, err := s.Title(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Example" {
		t.Fatalf("title = %q", title)
	}
}

func TestSessionScreenshot(t *testing.T) {
	c := startFakeCDP(t)
	s := &Session{ws: c, pending: map[int64]chan cdpResult{}}
	defer s.Close()
	go s.readLoop()

	raw, err := s.Screenshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "fake-png-bytes" {
		t.Fatalf("screenshot = %q", raw)
	}
}

func TestSessionCDPError(t *testing.T) {
	c := startFakeCDP(t)
	s := &Session{ws: c, pending: map[int64]chan cdpResult{}}
	defer s.Close()
	go s.readLoop()

	_, err := s.call(context.Background(), "Nope.nope", nil)
	if err == nil {
		t.Fatal("expected CDP error")
	}
}

func TestFindChromiumMissing(t *testing.T) {
	t.Setenv("GG_CHROMIUM", "/nonexistent/chromium-xyz")
	if _, err := FindChromium(); err == nil {
		t.Fatal("expected error when no chromium exists")
	} else if got := err.Error(); !contains(got, "GG_CHROMIUM") {
		t.Fatalf("error should mention GG_CHROMIUM: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// ---- fake CDP websocket server ----

func startFakeWSServer(t *testing.T) string {
	t.Helper()
	ln, addr := listenTCP(t)
	go serveFakeCDP(ln)
	t.Cleanup(func() { ln.Close() })
	return addr
}

func TestPageTargetURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"type": "browser", "webSocketDebuggerUrl": "ws://x/devtools/browser/1"},
			{"type": "page", "webSocketDebuggerUrl": "ws://x/devtools/page/2"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// pageTargetURL takes a ws:// browser URL; swap the scheme to match the test server.
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "/devtools/browser/1"
	got, err := pageTargetURL(context.Background(), wsURL)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ws://x/devtools/page/2" {
		t.Fatalf("got %q", got)
	}
}

func TestPageTargetURLNone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"type": "browser", "webSocketDebuggerUrl": "ws://x/devtools/browser/1"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "/devtools/browser/1"
	if _, err := pageTargetURL(context.Background(), wsURL); err == nil {
		t.Fatal("expected error when no page target")
	}
}
