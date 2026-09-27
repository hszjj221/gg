package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/artifact"
)

func openArtifactStore(t *testing.T) *artifact.Store {
	t.Helper()
	s, err := artifact.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestArtifactCreateTool(t *testing.T) {
	s := openArtifactStore(t)
	tool := NewArtifactCreateTool(s)
	if tool.Name() != "artifact_create" {
		t.Fatalf("name = %q", tool.Name())
	}
	res := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"title": "Trip", "type": "markdown", "content": "# Tokyo",
	}))
	if res.IsError {
		t.Fatalf("execute failed: %v", res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "created artifact") || !strings.Contains(text, "v1") {
		t.Fatalf("result = %q", text)
	}
	// Invalid type surfaces as a tool error, not a panic.
	res = tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"title": "Trip", "type": "pdf", "content": "x",
	}))
	if !res.IsError {
		t.Fatal("expected error for unsupported type")
	}
}

func TestArtifactEditTool(t *testing.T) {
	s := openArtifactStore(t)
	a, err := s.Create("Trip", artifact.TypeMarkdown, "# v1")
	if err != nil {
		t.Fatal(err)
	}
	tool := NewArtifactEditTool(s)
	res := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"artifact_id": a.ID, "content": "# v2",
	}))
	if res.IsError {
		t.Fatalf("execute failed: %v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "v2") {
		t.Fatalf("result = %q", res.Content[0].Text)
	}
	res = tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"artifact_id": "nope", "content": "x",
	}))
	if !res.IsError {
		t.Fatal("expected error for unknown id")
	}
}

func TestArtifactToolApproval(t *testing.T) {
	s := openArtifactStore(t)
	create := NewArtifactCreateTool(s)
	req, err := create.ApprovalRequest(mustJSON(t, map[string]any{
		"title": "Trip", "type": "html", "content": "<h1>hi</h1>",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolName != "artifact_create" || !strings.Contains(req.Summary, "Trip") {
		t.Fatalf("approval = %+v", req)
	}
	if !strings.Contains(req.Details, "<h1>hi</h1>") {
		t.Fatalf("approval details missing content preview: %q", req.Details)
	}

	a, _ := s.Create("Trip", artifact.TypeMarkdown, "# v1")
	edit := NewArtifactEditTool(s)
	req, err = edit.ApprovalRequest(mustJSON(t, map[string]any{
		"artifact_id": a.ID, "content": "# v2",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Details, "# v1") || !strings.Contains(req.Details, "# v2") {
		t.Fatalf("edit approval should preview before/after: %q", req.Details)
	}
}
