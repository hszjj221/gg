package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hszjj221/gg/internal/agent"
)

// mcpCallTimeout bounds one tools/call round-trip. The dial itself is
// bounded by the caller's context.
const mcpCallTimeout = 60 * time.Second

// previewLimit caps the argument preview shown in approval requests.
const previewLimit = 1200

// MCPTool adapts one tool from an MCP server to agent.Tool.
//
// It always implements agent.ApprovalDescriber: MCP servers are external
// capabilities, so every call passes through the approval pipeline (the
// unattended approver denies them unless the job opted in).
type MCPTool struct {
	server      string // MCP server name from mcp.json
	remote      string // original tool name on the server
	name        string // sanitized agent-visible name
	description string
	parameters  map[string]any
	session     session
}

var (
	_ agent.Tool              = (*MCPTool)(nil)
	_ agent.ApprovalDescriber = (*MCPTool)(nil)
)

// newMCPTool builds the adapter. name must already be sanitized and unique
// within the agent's toolset (the Connector owns collision handling).
func newMCPTool(server string, remote *mcp.Tool, name string, sess session) *MCPTool {
	return &MCPTool{
		server:      server,
		remote:      remote.Name,
		name:        name,
		description: truncateRunes(remote.Description, descriptionTruncateRunes),
		parameters:  NormalizeInputSchema(remote.InputSchema),
		session:     sess,
	}
}

// Name returns the sanitized tool name, e.g. "mcp_filesystem_read_file".
func (t *MCPTool) Name() string { return t.name }

// Definition exposes the tool for OpenAI-style function calling.
func (t *MCPTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        t.name,
		Description: t.description,
		Parameters:  t.parameters,
	}
}

// Execute calls the remote tool and converts the MCP result content into
// text blocks. A protocol-level tool error (result.isError) becomes an
// agent tool error so the model sees the failure distinctly.
func (t *MCPTool) Execute(ctx context.Context, args json.RawMessage) agent.ToolResult {
	var argv map[string]any
	if len(args) != 0 {
		if err := json.Unmarshal(args, &argv); err != nil {
			return errorResult(fmt.Errorf("mcp %s: invalid arguments: %w", t.name, err))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, mcpCallTimeout)
	defer cancel()
	res, err := t.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.remote,
		Arguments: argv,
	})
	if err != nil {
		return errorResult(fmt.Errorf("mcp %s: call failed: %w", t.name, err))
	}
	return resultFromContent(t.name, res)
}

// ApprovalRequest describes the call for the human approver. Because the
// tool always implements ApprovalDescriber, the runner asks for approval
// on every MCP call — there is no silent external execution.
func (t *MCPTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	return agent.ApprovalRequest{
		ToolName:  t.name,
		Summary:   fmt.Sprintf("MCP %s.%s", t.server, t.remote),
		Details:   "arguments: " + previewJSON(raw),
		Arguments: raw,
	}, nil
}

func resultFromContent(name string, res *mcp.CallToolResult) agent.ToolResult {
	var b strings.Builder
	for _, c := range res.Content {
		switch content := c.(type) {
		case *mcp.TextContent:
			b.WriteString(content.Text)
			b.WriteByte('\n')
		default:
			fmt.Fprintf(&b, "[non-text content: %T]\n", c)
		}
	}
	text := strings.TrimRight(b.String(), "\n")
	if res.IsError && text == "" {
		return errorResult(fmt.Errorf("mcp %s: tool reported an error with no content", name))
	}
	result := textResult(text)
	result.IsError = res.IsError
	return result
}

func previewJSON(raw json.RawMessage) string {
	s := truncateRunes(strings.TrimSpace(string(raw)), previewLimit)
	if s == "" {
		return "{}"
	}
	return s
}

func textResult(text string) agent.ToolResult {
	return agent.ToolResult{Content: []agent.ContentBlock{{Type: agent.ContentText, Text: text}}}
}

func errorResult(err error) agent.ToolResult {
	return agent.ToolResult{IsError: true, Content: []agent.ContentBlock{{Type: agent.ContentText, Text: err.Error()}}}
}
