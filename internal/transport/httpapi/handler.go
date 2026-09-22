package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/transport/jsonrpc"
)

const maxRequestBytes = 4 * 1024 * 1024

type Handler struct {
	rpc       *jsonrpc.Handler
	workspace *app.Workspace
	token     string
}

func NewHandler(rpc *jsonrpc.Handler, workspace *app.Workspace, token string) *Handler {
	return &Handler{rpc: rpc, workspace: workspace, token: token}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocolVersion": jsonrpc.ProtocolVersion})
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
	after, err := strconv.ParseInt(defaultString(r.URL.Query().Get("after"), "0"), 10, 64)
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
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	for {
		events, done, err := h.workspace.WaitRun(r.Context(), runID, after)
		if err != nil {
			writeSSE(w, "error", jsonrpc.ErrorFrom(err))
			flusher.Flush()
			return
		}
		for _, event := range events {
			writeSSE(w, "event", event)
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

func writeSSE(w io.Writer, eventType string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
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
