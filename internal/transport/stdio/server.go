package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"

	"github.com/hszjj221/gg/internal/transport/jsonrpc"
)

type Server struct {
	handler *jsonrpc.Handler
	input   io.Reader
	output  io.Writer
	// Log optionally receives panic reports from request handlers. Nil
	// disables logging; the daemon sets it to its stderr logger.
	Log *slog.Logger
}

func NewServer(handler *jsonrpc.Handler, input io.Reader, output io.Writer) *Server {
	return &Server{handler: handler, input: input, output: output}
}

// Serve processes newline-delimited JSON-RPC concurrently. Concurrent handling
// is required so a pending run.wait request cannot block run.approve or cancel.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	scanner := bufio.NewScanner(s.input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	encoder := json.NewEncoder(s.output)
	var writes sync.Mutex
	var requests sync.WaitGroup

	write := func(response jsonrpc.Response) {
		writes.Lock()
		defer writes.Unlock()
		_ = encoder.Encode(response)
	}
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		requests.Add(1)
		go func() {
			defer requests.Done()
			serveRequest(ctx, line, s.handler.Handle, write, s.Log)
		}()
	}
	cancel()
	requests.Wait()
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read JSON-RPC request: %w", err)
	}
	return nil
}

// serveRequest handles one JSON-RPC line. A panic in the handler is
// recovered, logged with a stack trace, and converted into a -32603
// (internal error) response so one bad request can never kill the daemon;
// the server keeps serving subsequent requests.
func serveRequest(ctx context.Context, line []byte, handle func(context.Context, jsonrpc.Request) jsonrpc.Response, write func(jsonrpc.Response), log *slog.Logger) {
	var request jsonrpc.Request
	defer func() {
		if r := recover(); r != nil {
			if log != nil {
				log.Error("stdio request panicked",
					"panic", fmt.Sprintf("%v", r), "stack", string(debug.Stack()))
			}
			// Notifications never get a response, even on panic: an
			// unsolicited reply would desynchronize the client.
			if len(request.ID) != 0 {
				write(jsonrpc.Response{JSONRPC: jsonrpc.Version, ID: request.ID,
					Error: &jsonrpc.Error{Code: -32603, Message: "internal error"}})
			}
		}
	}()
	if err := json.Unmarshal(line, &request); err != nil {
		write(jsonrpc.Response{JSONRPC: jsonrpc.Version, Error: &jsonrpc.Error{Code: -32700, Message: "parse error: " + err.Error()}})
		return
	}
	response := handle(ctx, request)
	if len(request.ID) != 0 {
		write(response)
	}
}
