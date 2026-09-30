package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// implementationVersion is informational only, sent to MCP servers during
// initialization. It tracks the gg CLI version.
const implementationVersion = "0.1.0"

// session is the narrow slice of the MCP client session the bridge needs.
// *mcp.ClientSession satisfies it; tests substitute a fake.
type session interface {
	ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
	Close() error
}

var _ session = (*mcp.ClientSession)(nil)

// dialFunc connects to one configured server. It is a variable so the
// Connector can substitute a fake in tests.
//
// initCtx bounds initialization (connect handshake); lifeCtx bounds the
// server subprocess's lifetime and must outlive any single turn — see
// Connector.lifeCtx.
type dialFunc func(initCtx, lifeCtx context.Context, name string, sc ServerConfig) (session, error)

// dialServer connects to a single MCP server over stdio or streamable
// HTTP and returns the initialized session.
func dialServer(initCtx, lifeCtx context.Context, name string, sc ServerConfig) (session, error) {
	var transport mcp.Transport
	switch {
	case sc.Command != "":
		transport = stdioTransport(lifeCtx, sc)
	case sc.URL != "":
		transport = &mcp.StreamableClientTransport{Endpoint: sc.URL}
	default:
		return nil, fmt.Errorf("mcp server %q: neither command nor url set", name)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "gg", Version: implementationVersion}, nil)
	sess, err := client.Connect(initCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: connect: %w", name, err)
	}
	return sess, nil
}

// stdioTransport builds a CommandTransport for a subprocess server.
// The process is parented to lifeCtx (the Connector's lifetime), not the
// calling turn's context, so it survives turn cancellation and is reaped
// by Connector.Close. stdout is reserved for JSON-RPC framing, so stderr
// is discarded: server logs must never leak into the protocol stream.
func stdioTransport(lifeCtx context.Context, sc ServerConfig) *mcp.CommandTransport {
	cmd := exec.CommandContext(lifeCtx, sc.Command, sc.Args...)
	cmd.Env = subprocessEnv(sc.Env)
	// Never inherit the parent environment wholesale: API keys and tokens
	// in gg's environment must not leak into server subprocesses. Servers
	// that need secrets declare them in the per-server env map.
	cmd.Stderr = io.Discard
	return &mcp.CommandTransport{Command: cmd}
}

// subprocessEnv builds the minimal environment for a server subprocess:
// PATH/HOME (+SYSTEMROOT on Windows) plus the server's configured env.
func subprocessEnv(extra map[string]string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	if runtime.GOOS == "windows" {
		env = append(env,
			"USERPROFILE="+os.Getenv("USERPROFILE"),
			"SYSTEMROOT="+os.Getenv("SYSTEMROOT"),
		)
	}
	for k, v := range extra {
		if k != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}
