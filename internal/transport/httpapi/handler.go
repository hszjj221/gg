package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/transport/jsonrpc"
)

const maxRequestBytes = 4 * 1024 * 1024
const sseHeartbeatInterval = 15 * time.Second

type Handler struct {
	rpc   *jsonrpc.Handler
	rt    *app.Runtime
	token string
	// channelStatus, when set, reports daemon background-channel states on
	// the authenticated /health endpoint. It is a callback (not a concrete
	// daemon type) so this transport package does not import the daemon.
	channelStatus func() []ChannelStatus
}

// ChannelStatus is the last known runtime state of one daemon background
// channel (scheduler, telegram, ...), served on /health.
type ChannelStatus struct {
	Name     string `json:"name"`
	State    string `json:"state"` // running | failed | stopped
	Error    string `json:"error,omitempty"`
	FailedAt string `json:"failedAt,omitempty"`
}

func NewHandler(rpc *jsonrpc.Handler, rt *app.Runtime, token string) *Handler {
	return &Handler{rpc: rpc, rt: rt, token: token}
}

// SetChannelStatus installs the callback backing the "channels" field of
// /health. A nil callback omits the field.
func (h *Handler) SetChannelStatus(fn func() []ChannelStatus) {
	h.channelStatus = fn
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Answer CORS preflights from local UI origins before the bearer-token
	// gate: the packaged Electron renderer loads from file:// (its origin
	// serializes as "null") and the transport sends Authorization plus JSON
	// content headers, so Chromium issues an OPTIONS preflight that carries
	// no token. The bearer token stays the real authentication boundary;
	// CORS only tells the browser it may deliver the gg UI's own requests.
	if localUIOriginAllowed(w, r) && r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Cache-Control, Last-Event-ID")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// /healthz is the unauthenticated liveness probe (Kubernetes-style):
	// it answers whether the process is alive and serving HTTP. It carries
	// no sensitive data, so it sits outside the bearer-token gate. /health
	// stays authenticated and reports API readiness (protocol version).
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gg"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/health":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		body := map[string]any{"ok": true, "protocolVersion": jsonrpc.ProtocolVersion}
		if h.channelStatus != nil {
			body["channels"] = h.channelStatus()
		}
		writeJSON(w, http.StatusOK, body)
	case "/rpc":
		h.handleRPC(w, r)
	case "/events":
		h.handleEvents(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes+1))
	var request jsonrpc.Request
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, jsonrpc.Response{JSONRPC: jsonrpc.Version, Error: &jsonrpc.Error{Code: -32700, Message: "parse error: " + err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, h.rpc.Handle(r.Context(), request))
}

func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("runId"))
	if runID == "" {
		http.Error(w, "runId is required", http.StatusBadRequest)
		return
	}
	afterValue := r.URL.Query().Get("after")
	if afterValue == "" {
		afterValue = r.Header.Get("Last-Event-ID")
	}
	after, err := strconv.ParseInt(defaultString(afterValue, "0"), 10, 64)
	if err != nil || after < 0 {
		http.Error(w, "after must be a non-negative integer", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	for {
		waitContext, cancel := context.WithTimeout(r.Context(), sseHeartbeatInterval)
		events, done, err := h.rt.WaitRun(waitContext, runID, after)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				_, _ = io.WriteString(w, ": keepalive\n\n")
				flusher.Flush()
				continue
			}
			if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
				return
			}
			writeSSE(w, "error", "", jsonrpc.ErrorFrom(err))
			flusher.Flush()
			return
		}
		for _, event := range events {
			writeSSE(w, "event", strconv.FormatInt(event.Sequence, 10), event)
			after = event.Sequence
		}
		flusher.Flush()
		if done {
			return
		}
	}
}

func (h *Handler) authorized(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if len(provided) != len(h.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

// localUIOriginAllowed reports whether the request comes from a local gg UI
// and, if so, marks the response shareable with that origin. The packaged
// Electron renderer loads from file://, whose origin serializes as "null";
// loopback http(s) origins cover dev servers. Anything else (including a
// missing Origin, i.e. non-browser clients) gets no CORS headers, so the
// daemon never widens its attack surface for remote web pages.
func localUIOriginAllowed(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || !isLocalUIOrigin(origin) {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	return true
}

func isLocalUIOrigin(origin string) bool {
	if origin == "null" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func writeSSE(w io.Writer, eventType, eventID string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	if eventID != "" {
		_, _ = fmt.Fprintf(w, "id: %s\n", eventID)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
