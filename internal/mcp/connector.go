package mcp

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hszjj221/gg/internal/agent"
)

// Connector manages the MCP servers declared in mcp.json for one Service.
// Connections are established lazily on first use and kept for the
// Service's lifetime; Close terminates every server session (including
// stdio subprocesses).
//
// A server that fails to dial or list is skipped and never retried within
// the Connector's lifetime, so one broken server cannot slow every turn.
// (Caller cancellation is never recorded as a failure: the next turn
// retries.) Servers removed from the config are disconnected on the next
// Tools call; servers whose config changed are reconnected.
type Connector struct {
	configPath string
	dial       dialFunc

	// lifeCtx bounds server subprocess lifetimes to the Connector's own
	// lifetime. A stdio server first discovered during a turn must not die
	// with that turn's context while the Connector keeps serving its
	// cached tools — hence the process is NOT parented to the caller's
	// ctx. Only initialization (dial + tools/list) is bounded by it.
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	mu        sync.Mutex
	sessions  map[string]session
	configs   map[string]ServerConfig
	toolLists map[string][]agent.Tool
	failed    map[string]bool
	failErr   map[string]error
	usedNames map[string]bool
}

// NewConnector creates a Connector reading its server list from configPath
// (~/.gg/mcp.json). No I/O happens until the first Tools call.
func NewConnector(configPath string) *Connector {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &Connector{
		configPath: configPath,
		dial:       dialServer,
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
		sessions:   map[string]session{},
		configs:    map[string]ServerConfig{},
		toolLists:  map[string][]agent.Tool{},
		failed:     map[string]bool{},
		failErr:    map[string]error{},
		usedNames:  map[string]bool{},
	}
}

// Tools reconciles the configured servers and returns the adapted tools.
// It is safe for concurrent use; the first call may dial servers and is
// the only one that performs network or subprocess I/O for new servers.
func (c *Connector) Tools(ctx context.Context) ([]agent.Tool, error) {
	cfg, err := Load(c.configPath)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lifeCtx.Err() != nil {
		return nil, errors.New("MCP connector is closed")
	}

	// Disconnect servers removed from the config, or whose config changed
	// (a stale subprocess must not keep serving old tools).
	for name, previous := range c.configs {
		sc, ok := cfg.Servers[name]
		if !ok || !reflect.DeepEqual(previous, sc) {
			if sess := c.sessions[name]; sess != nil {
				_ = sess.Close()
			}
			delete(c.sessions, name)
			delete(c.configs, name)
			delete(c.toolLists, name)
			delete(c.failed, name)
			delete(c.failErr, name)
		}
	}
	// Rebuild the used-name set from the surviving servers so a removed
	// server frees its names deterministically.
	c.usedNames = map[string]bool{}
	for _, tl := range c.toolLists {
		for _, t := range tl {
			c.usedNames[t.Name()] = true
		}
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []agent.Tool
	for _, name := range names {
		sc := cfg.Servers[name]
		tl, ok := c.toolLists[name]
		if !ok && !c.failed[name] {
			tl, ok = c.connectLocked(ctx, name, sc)
		}
		out = append(out, tl...)
	}
	return out, nil
}

// FailedServers returns the servers that failed to dial or list, mapped
// to the error that stopped them. A server stays listed until its config
// changes or it is removed; callers use this to log per-server failures
// that otherwise leave no trace (the provider still returns the working
// servers' tools, so this is not a Build error).
func (c *Connector) FailedServers() map[string]error {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]error, len(c.failErr))
	for name, err := range c.failErr {
		out[name] = err
	}
	return out
}

// connectLocked dials one server, lists its tools (all pages), and adapts
// them. Callers must hold c.mu. A genuine failure marks the server failed:
// it is skipped on later calls without retrying. Caller cancellation is
// not a server failure — the next turn retries.
func (c *Connector) connectLocked(ctx context.Context, name string, sc ServerConfig) ([]agent.Tool, bool) {
	c.configs[name] = sc
	sess, err := c.dial(ctx, c.lifeCtx, name, sc)
	if err != nil {
		if ctx.Err() == nil {
			c.failed[name] = true
			c.failErr[name] = err
		}
		return nil, false
	}
	remote, err := listAllTools(ctx, sess)
	if err != nil {
		_ = sess.Close()
		if ctx.Err() == nil {
			c.failed[name] = true
			c.failErr[name] = err
		}
		return nil, false
	}
	c.sessions[name] = sess
	c.configs[name] = sc
	tl := make([]agent.Tool, 0, len(remote))
	for _, rt := range remote {
		if rt == nil {
			continue
		}
		tl = append(tl, newMCPTool(name, rt, c.uniqueName(name, rt.Name), sess))
	}
	c.toolLists[name] = tl
	return tl, true
}

// listAllTools fetches every page of tools/list; servers may paginate.
func listAllTools(ctx context.Context, sess session) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	cursor := ""
	for {
		res, err := sess.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			return out, nil
		}
		cursor = res.NextCursor
	}
}

// uniqueName sanitizes server+tool and appends a numeric suffix on
// collision, so two servers can never claim the same agent tool name.
func (c *Connector) uniqueName(server, tool string) string {
	base := SanitizeToolName(server, tool)
	name := base
	for i := 2; c.usedNames[name]; i++ {
		name = truncateRunes(base, maxToolNameLength-4) + "-" + strconv.Itoa(i)
	}
	c.usedNames[name] = true
	return name
}

// Close terminates every connected server session and cancels the
// connector-lifetime context, reaping stdio subprocesses. It is
// idempotent.
func (c *Connector) Close() error {
	c.lifeCancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for name, sess := range c.sessions {
		if err := sess.Close(); err != nil {
			errs = append(errs, err)
		}
		delete(c.sessions, name)
		delete(c.configs, name)
		delete(c.toolLists, name)
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
