// Package browser drives a headless Chromium over CDP (Chrome DevTools
// Protocol) with a hand-rolled WebSocket client: navigate, read page text,
// take screenshots. Fill-form automation comes later; read+screenshot first.
package browser

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/browser/ws"
)

// Session owns one headless Chromium + CDP connection.
type Session struct {
	cmd        *exec.Cmd
	ws         *ws.Conn
	profileDir string
	mu         sync.Mutex
	nextID     int64
	pending    map[int64]chan cdpResult
	closed     bool
}

type cdpResult struct {
	result json.RawMessage
	err    *cdpError
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpMessage struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

// chromiumCandidates returns likely Chromium binary paths for this OS
// (excluding an explicit GG_CHROMIUM, which FindChromium handles first).
func chromiumCandidates() []string {
	var out []string
	switch runtime.GOOS {
	case "darwin":
		out = append(out,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	default:
		out = append(out, "chromium", "chromium-browser", "google-chrome", "google-chrome-stable")
	}
	return out
}

func FindChromium() (string, error) {
	// An explicit GG_CHROMIUM is authoritative: fail instead of silently
	// falling back to a different binary.
	if p := os.Getenv("GG_CHROMIUM"); p != "" {
		if _, err := exec.LookPath(p); err == nil {
			return p, nil
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("browser: GG_CHROMIUM=%q not found", p)
	}
	for _, c := range chromiumCandidates() {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("browser: no Chromium found (tried %s); set GG_CHROMIUM to the binary path",
		strings.Join(chromiumCandidates(), ", "))
}

// Start launches headless Chromium with remote debugging and connects.
// The browser process is detached from ctx: it lives until Close is called,
// so a session can span multiple agent turns. Callers must call Close.
func Start(ctx context.Context) (*Session, error) {
	bin, err := FindChromium()
	if err != nil {
		return nil, err
	}
	profileDir, err := os.MkdirTemp("", "gg-chromium-*")
	if err != nil {
		return nil, err
	}
	stderr, err := os.Create(filepath.Join(profileDir, "stderr.log"))
	if err != nil {
		os.RemoveAll(profileDir)
		return nil, err
	}
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--remote-debugging-port=0",
		"--user-data-dir=" + profileDir,
		"about:blank",
	}
	// The sandbox is a meaningful security boundary when visiting arbitrary
	// pages; only disable it when explicitly asked (e.g. containers where
	// the kernel forbids sandboxing).
	if os.Getenv("GG_BROWSER_NO_SANDBOX") == "1" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	// Detached from ctx: killing the browser is Close's job, not the
	// caller's context cancellation.
	cmd := exec.Command(bin, args...)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		stderr.Close()
		os.RemoveAll(profileDir)
		return nil, fmt.Errorf("browser: launch chromium: %w", err)
	}
	wsURL, derr := waitForDevTools(ctx, stderr)
	if derr != nil {
		cmd.Process.Kill()
		stderr.Close()
		os.RemoveAll(profileDir)
		return nil, derr
	}
	conn, err := ws.Dial(wsURL, 10*time.Second)
	if err != nil {
		cmd.Process.Kill()
		stderr.Close()
		os.RemoveAll(profileDir)
		return nil, fmt.Errorf("browser: dial CDP: %w", err)
	}
	s := &Session{cmd: cmd, ws: conn, profileDir: profileDir, pending: map[int64]chan cdpResult{}}
	go s.readLoop()
	// Enable the domains we use.
	if _, err := s.call(ctx, "Page.enable", nil); err != nil {
		s.Close()
		return nil, err
	}
	if _, err := s.call(ctx, "Runtime.enable", nil); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// waitForDevTools scans the Chromium stderr log for the DevTools websocket URL,
// then resolves a page target via /json/list (the stderr URL is the
// browser-level endpoint, which does not accept Page/Runtime commands).
func waitForDevTools(ctx context.Context, f *os.File) (string, error) {
	browserURL, err := waitForBrowserURL(ctx, f)
	if err != nil {
		return "", err
	}
	return pageTargetURL(ctx, browserURL)
}

func waitForBrowserURL(ctx context.Context, f *os.File) (string, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		f.Seek(0, io.SeekStart)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if i := strings.Index(line, "ws://"); i >= 0 {
				u := strings.TrimSpace(line[i:])
				// The line may carry trailing text; the URL ends at whitespace.
				if j := strings.IndexAny(u, " \t"); j >= 0 {
					u = u[:j]
				}
				return u, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("browser: timed out waiting for DevTools endpoint")
}

// pageTargetURL asks the browser's /json/list for a page target and returns
// its webSocketDebuggerUrl.
func pageTargetURL(ctx context.Context, browserURL string) (string, error) {
	u, err := url.Parse(browserURL)
	if err != nil {
		return "", fmt.Errorf("browser: parse DevTools URL: %w", err)
	}
	u.Scheme = "http"
	u.Path = "/json/list"
	u.RawQuery = ""
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("browser: list targets: %w", err)
	}
	defer resp.Body.Close()
	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&targets); err != nil {
		return "", fmt.Errorf("browser: decode targets: %w", err)
	}
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("browser: no page target found")
}

func (s *Session) readLoop() {
	for {
		msg, err := s.ws.ReadText()
		if err != nil {
			s.failAll(err)
			return
		}
		var m cdpMessage
		if err := json.Unmarshal(msg, &m); err != nil {
			continue
		}
		if m.ID == 0 {
			continue // event notification; ignored for now
		}
		s.mu.Lock()
		ch, ok := s.pending[m.ID]
		delete(s.pending, m.ID)
		s.mu.Unlock()
		if ok {
			ch <- cdpResult{result: m.Result, err: m.Error}
		}
	}
}

func (s *Session) failAll(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.pending {
		ch <- cdpResult{err: &cdpError{Message: err.Error()}}
		delete(s.pending, id)
	}
}

func (s *Session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("browser: session closed")
	}
	s.nextID++
	id := s.nextID
	ch := make(chan cdpResult, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	var praw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		praw = b
	}
	req, _ := json.Marshal(cdpMessage{ID: id, Method: method, Params: praw})
	if err := s.ws.WriteText(req); err != nil {
		return nil, fmt.Errorf("browser: send %s: %w", method, err)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("browser: %s: %s", method, r.err.Message)
		}
		return r.result, nil
	}
}

// Navigate loads url and waits for the load event (up to timeout).
func (s *Session) Navigate(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := s.call(ctx, "Page.navigate", map[string]any{"url": url})
	if err != nil {
		return err
	}
	// Surface navigation failures (DNS/TLS/refused): CDP reports them in
	// the result's errorText, not as a protocol error.
	var navRes struct {
		LoaderID  string `json:"loaderId"`
		ErrorText string `json:"errorText"`
	}
	if json.Unmarshal(raw, &navRes) == nil && navRes.ErrorText != "" {
		return fmt.Errorf("browser: navigation to %s failed: %s", url, navRes.ErrorText)
	}
	// Wait for the NEW document: poll until readyState is complete AND the
	// URL matches (the old document, e.g. about:blank, may already be
	// "complete" before the new one commits). URLs are normalized for
	// comparison (trailing slash, fragment).
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("browser: navigation to %s timed out", url)
		default:
		}
		state, uerr := s.Evaluate(ctx, "document.readyState")
		if uerr != nil {
			return uerr
		}
		href, herr := s.Evaluate(ctx, "location.href")
		if herr != nil {
			return herr
		}
		if state == `"complete"` && normalizeURL(strings.Trim(href, `"`)) == normalizeURL(url) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// normalizeURL trims fragments and trailing slashes for navigation
// comparison.
func normalizeURL(u string) string {
	if i := strings.Index(u, "#"); i >= 0 {
		u = u[:i]
	}
	return strings.TrimSuffix(u, "/")
}

// Evaluate runs JS in the page and returns the JSON-encoded value.
func (s *Session) Evaluate(ctx context.Context, expr string) (string, error) {
	raw, err := s.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return string(out.Result.Value), nil
}

// Text returns the rendered page text (innerText), trimmed.
func (s *Session) Text(ctx context.Context) (string, error) {
	v, err := s.Evaluate(ctx, "document.body ? document.body.innerText : ''")
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal([]byte(v), &text); err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// Title returns document.title.
func (s *Session) Title(ctx context.Context) (string, error) {
	v, err := s.Evaluate(ctx, "document.title")
	if err != nil {
		return "", err
	}
	var title string
	if err := json.Unmarshal([]byte(v), &title); err != nil {
		return "", err
	}
	return title, nil
}

// Screenshot captures the viewport as PNG bytes.
func (s *Session) Screenshot(ctx context.Context) ([]byte, error) {
	raw, err := s.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"})
	if err != nil {
		return nil, err
	}
	var out struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Data)
}

// ScreenshotToFile captures the viewport and saves it under dir.
func (s *Session) ScreenshotToFile(ctx context.Context, dir string) (string, error) {
	raw, err := s.Screenshot(ctx)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102-150405.000000")+".png")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Close shuts down the CDP connection and the Chromium process.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.failAll(fmt.Errorf("browser: session closed"))
	_ = s.ws.Close()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	if s.profileDir != "" {
		os.RemoveAll(s.profileDir)
	}
	return nil
}

// DevToolsTargets is a tiny helper for tests: list /json/list targets.
func DevToolsTargets(httpPort int) ([]map[string]any, error) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", httpPort))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []map[string]any
	return out, json.NewDecoder(resp.Body).Decode(&out)
}
