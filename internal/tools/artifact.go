package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
)

// ArtifactCreateTool lets the agent create a versioned deliverable
// (markdown document or HTML page) in the artifact store.
type ArtifactCreateTool struct {
	store *artifact.Store
}

func NewArtifactCreateTool(store *artifact.Store) ArtifactCreateTool {
	return ArtifactCreateTool{store: store}
}

func (t ArtifactCreateTool) Name() string { return "artifact_create" }

func (t ArtifactCreateTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "artifact_create",
		Description: "Create a versioned deliverable (artifact) the user can open and read, e.g. a travel plan, report, or page. Returns the artifact id and version.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":   map[string]any{"type": "string", "description": "Short human-readable title."},
				"type":    map[string]any{"type": "string", "enum": []string{"markdown", "html"}, "description": "Artifact type: markdown document or standalone HTML page."},
				"content": map[string]any{"type": "string", "description": "Full artifact content (markdown source or HTML)."},
			},
			"required": []string{"title", "type", "content"},
		},
	}
}

type artifactCreateInput struct {
	Title   string `json:"title"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

func (t ArtifactCreateTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input artifactCreateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid artifact_create arguments: %w", err)
	}
	return agent.ApprovalRequest{
		ToolName:  "artifact_create",
		Summary:   fmt.Sprintf("create %s artifact %q (%d bytes)", input.Type, input.Title, len(input.Content)),
		Details:   fmt.Sprintf("title: %s\ntype: %s\ncontent bytes: %d\n\n%s", input.Title, input.Type, len(input.Content), contentChangePreview("", input.Content)),
		Arguments: raw,
	}, nil
}

func (t ArtifactCreateTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	if err := ctx.Err(); err != nil {
		return errorResult(err)
	}
	var input artifactCreateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid artifact_create arguments: %w", err))
	}
	a, err := t.store.Create(input.Title, input.Type, input.Content)
	if err != nil {
		return errorResult(err)
	}
	return textResult(fmt.Sprintf("created artifact %s (v1): %q. The user can open it from the Artifacts view.", a.ID, a.Title))
}

// ArtifactEditTool lets the agent publish a new version of an artifact.
type ArtifactEditTool struct {
	store *artifact.Store
}

func NewArtifactEditTool(store *artifact.Store) ArtifactEditTool {
	return ArtifactEditTool{store: store}
}

func (t ArtifactEditTool) Name() string { return "artifact_edit" }

func (t ArtifactEditTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "artifact_edit",
		Description: "Replace an artifact's content with a new version (old versions are kept). Returns the new version number.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"artifact_id": map[string]any{"type": "string", "description": "Artifact id from artifact_create."},
				"content":     map[string]any{"type": "string", "description": "Full new content (replaces the previous version)."},
			},
			"required": []string{"artifact_id", "content"},
		},
	}
}

type artifactEditInput struct {
	ArtifactID string `json:"artifact_id"`
	Content    string `json:"content"`
}

func (t ArtifactEditTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input artifactEditInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid artifact_edit arguments: %w", err)
	}
	before := ""
	if a, err := t.store.Get(input.ArtifactID); err == nil {
		before, _ = t.store.ReadVersion(a.ID, a.Version)
	}
	return agent.ApprovalRequest{
		ToolName:  "artifact_edit",
		Summary:   fmt.Sprintf("update artifact %s (%d bytes)", input.ArtifactID, len(input.Content)),
		Details:   fmt.Sprintf("artifact: %s\ncontent bytes: %d\n\n%s", input.ArtifactID, len(input.Content), contentChangePreview(before, input.Content)),
		Arguments: raw,
	}, nil
}

func (t ArtifactEditTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	if err := ctx.Err(); err != nil {
		return errorResult(err)
	}
	var input artifactEditInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid artifact_edit arguments: %w", err))
	}
	a, err := t.store.AddVersion(input.ArtifactID, input.Content)
	if err != nil {
		return errorResult(err)
	}
	return textResult(fmt.Sprintf("updated artifact %s to v%d: %q.", a.ID, a.Version, a.Title))
}
