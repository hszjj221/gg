// Package mcp bridges external MCP (Model Context Protocol) servers into
// gg's agent tool surface.
//
// A Connector is configured from ~/.gg/mcp.json, dials each declared server
// (stdio subprocess or streamable HTTP), and adapts the servers' tools to
// agent.Tool. Adapted tools always implement agent.ApprovalDescriber.
// Unknown server consequences are classified fail-closed (R=false,
// E/M/U=true) with confirm risk, so every MCP call requires human
// confirmation; yolo, unattended allow-all, and standing grants cannot
// bypass it.
//
// The package never imports internal/app: the app layer consumes it through
// the ToolProvider registry only.
package mcp
