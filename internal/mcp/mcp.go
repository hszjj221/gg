// Package mcp bridges external MCP (Model Context Protocol) servers into
// gg's agent tool surface.
//
// A Connector is configured from ~/.gg/mcp.json, dials each declared server
// (stdio subprocess or streamable HTTP), and adapts the servers' tools to
// agent.Tool. Adapted tools always implement agent.ApprovalDescriber, so
// every MCP call passes through the normal approval pipeline — external
// capabilities are treated as write operations by default.
//
// The package never imports internal/app: the app layer consumes it through
// the ToolProvider registry only.
package mcp
