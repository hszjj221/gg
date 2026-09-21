package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/hszjj221/gg/internal/transport/jsonrpc"
)

type Server struct {
	handler *jsonrpc.Handler
	input   io.Reader
	output  io.Writer
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
			var request jsonrpc.Request
			if err := json.Unmarshal(line, &request); err != nil {
				write(jsonrpc.Response{JSONRPC: jsonrpc.Version, Error: &jsonrpc.Error{Code: -32700, Message: "parse error: " + err.Error()}})
				return
			}
			response := s.handler.Handle(ctx, request)
			if len(request.ID) != 0 {
				write(response)
			}
		}()
	}
	cancel()
	requests.Wait()
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read JSON-RPC request: %w", err)
	}
	return nil
}
