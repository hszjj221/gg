package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"gg/internal/agent"
)

type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

type Client struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func NewClient(config Config) *Client {
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		apiKey:  config.APIKey,
		baseURL: strings.TrimRight(config.BaseURL, "/"),
		model:   config.Model,
		http:    httpClient,
	}
}

func (c *Client) Complete(ctx context.Context, req agent.Request, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	if c.apiKey == "" {
		return agent.AssistantMessage{}, fmt.Errorf("missing API key: set OPENAI_API_KEY or pass --api-key")
	}
	if c.baseURL == "" {
		return agent.AssistantMessage{}, fmt.Errorf("missing base URL")
	}
	body, err := json.Marshal(c.requestPayload(req))
	if err != nil {
		return agent.AssistantMessage{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return agent.AssistantMessage{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return agent.AssistantMessage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return agent.AssistantMessage{}, fmt.Errorf("OpenAI-compatible API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return parseStream(resp.Body, onEvent)
}

func (c *Client) requestPayload(req agent.Request) map[string]any {
	payload := map[string]any{
		"model":    c.model,
		"messages": chatMessages(req.Messages),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = chatTools(req.Tools)
		payload["tool_choice"] = "auto"
	}
	return payload
}

func chatMessages(messages []agent.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		item := map[string]any{"role": string(msg.Role)}
		switch msg.Role {
		case agent.RoleTool:
			item["tool_call_id"] = msg.ToolCallID
			item["content"] = msg.Content
		case agent.RoleAssistant:
			item["content"] = msg.Content
			if len(msg.ToolCalls) > 0 {
				item["tool_calls"] = outboundToolCalls(msg.ToolCalls)
			}
		default:
			item["content"] = msg.Content
		}
		out = append(out, item)
	}
	return out
}

func outboundToolCalls(calls []agent.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		out = append(out, map[string]any{
			"id":   call.ID,
			"type": "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": string(call.Arguments),
			},
		})
	}
	return out
}

func chatTools(defs []agent.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        def.Name,
				"description": def.Description,
				"parameters":  def.Parameters,
			},
		})
	}
	return out
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls []deltaToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type deltaToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolCallBuilder struct {
	id   string
	name string
	args strings.Builder
}

func parseStream(r io.Reader, onEvent func(agent.Event)) (agent.AssistantMessage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var text strings.Builder
	toolCalls := map[int]*toolCallBuilder{}
	finishReason := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return agent.AssistantMessage{}, err
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				if onEvent != nil {
					onEvent(agent.Event{Type: agent.EventTextDelta, Text: choice.Delta.Content})
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				builder := toolCalls[tc.Index]
				if builder == nil {
					builder = &toolCallBuilder{}
					toolCalls[tc.Index] = builder
				}
				if tc.ID != "" {
					builder.id = tc.ID
				}
				if tc.Function.Name != "" {
					builder.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					builder.args.WriteString(tc.Function.Arguments)
				}
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return agent.AssistantMessage{}, err
	}

	content := text.String()
	msg := agent.AssistantMessage{
		Message: agent.Message{
			Role:    agent.RoleAssistant,
			Content: content,
		},
		StopReason: agent.StopReasonEndTurn,
	}
	if content != "" {
		msg.ContentBlocks = []agent.ContentBlock{{Type: agent.ContentText, Text: content}}
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = buildToolCalls(toolCalls)
		msg.StopReason = agent.StopReasonToolUse
	}
	if finishReason == "tool_calls" {
		msg.StopReason = agent.StopReasonToolUse
	}
	return msg, nil
}

func buildToolCalls(builders map[int]*toolCallBuilder) []agent.ToolCall {
	indexes := make([]int, 0, len(builders))
	for idx := range builders {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	out := make([]agent.ToolCall, 0, len(indexes))
	for _, idx := range indexes {
		builder := builders[idx]
		args := builder.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out = append(out, agent.ToolCall{
			ID:        builder.id,
			Name:      builder.name,
			Arguments: json.RawMessage(args),
		})
	}
	return out
}
