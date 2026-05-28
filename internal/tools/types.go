package tools

import "github.com/hszjj221/gg/internal/agent"

type Tool = agent.Tool
type ToolResult = agent.ToolResult
type ContentBlock = agent.ContentBlock

const ContentText = agent.ContentText

const (
	defaultMaxReadLines = 2000
	defaultMaxReadBytes = 256 * 1024
)

func textResult(text string) ToolResult {
	return ToolResult{Content: []ContentBlock{{Type: ContentText, Text: text}}}
}

func errorResult(err error) ToolResult {
	return ToolResult{IsError: true, Content: []ContentBlock{{Type: ContentText, Text: err.Error()}}}
}
