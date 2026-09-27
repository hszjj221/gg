package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/kb"
)

// KBSearchTool performs semantic search over a local knowledge base built
// with `gg kb index`. It is read-only: it embeds the query with the same
// model recorded in the index manifest and returns the top-k chunks ranked
// by cosine similarity.
type KBSearchTool struct {
	kbDir    string
	name     string
	apiKey   string
	baseURL  string
	topK     int
	embedder kb.Embedder // optional override, used by tests
}

// KBSearchOptions tunes the knowledge-base search tool.
type KBSearchOptions struct {
	// Name selects the knowledge base; empty means the default one.
	Name string
	// TopK caps results per call; zero selects the default.
	TopK int
	// Embedder overrides embedding construction; nil builds an
	// OpenAI-compatible embedder from the index manifest at call time.
	Embedder kb.Embedder
}

func NewKBSearchTool(kbDir, apiKey, baseURL string, opts KBSearchOptions) KBSearchTool {
	topK := opts.TopK
	if topK <= 0 {
		topK = 5
	}
	return KBSearchTool{
		kbDir:    kbDir,
		name:     opts.Name,
		apiKey:   apiKey,
		baseURL:  baseURL,
		topK:     topK,
		embedder: opts.Embedder,
	}
}

func (t KBSearchTool) Name() string { return "kb_search" }

func (t KBSearchTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "kb_search",
		Description: "Semantic search over the local knowledge base (built with `gg kb index`). Use it when the question is about project docs, design notes, or any ingested corpus rather than live code. Returns matching passages with source file and similarity score.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "natural-language search query"},
				"top_k": map[string]any{"type": "integer", "description": "max passages to return, defaults to 5"},
			},
			"required": []string{"query"},
		},
	}
}

func (t KBSearchTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid kb_search arguments: %w", err))
	}
	if strings.TrimSpace(input.Query) == "" {
		return errorResult(fmt.Errorf("query is required"))
	}
	topK := t.topK
	if input.TopK > 0 {
		topK = input.TopK
	}
	ix, err := kb.Load(t.kbDir, t.name)
	if err != nil {
		return errorResult(fmt.Errorf("kb_search: %v; build one with `gg kb index <dir>`", err))
	}
	if ix.EmbedBaseURL != "" && !kb.SameEndpoint(ix.EmbedBaseURL, t.baseURL) {
		return errorResult(fmt.Errorf("kb_search: index was built with embeddings endpoint %s, but the agent is configured with %s; set GG_EMBED_BASE_URL=%s (and GG_EMBED_API_KEY) or rebuild the index",
			ix.EmbedBaseURL, t.baseURL, ix.EmbedBaseURL))
	}
	emb := t.embedder
	if emb == nil {
		// Embed the query with the exact model the index was built with;
		// mixing embedding models silently corrupts similarity scores.
		emb = kb.NewOpenAIEmbedder(t.apiKey, t.baseURL, ix.Model)
	}
	vecs, err := emb.Embed(ctx, []string{input.Query})
	if err != nil {
		return errorResult(fmt.Errorf("kb_search: embed query: %w", err))
	}
	results, err := ix.Search(vecs[0], topK)
	if err != nil {
		return errorResult(fmt.Errorf("kb_search: %w", err))
	}
	if len(results) == 0 {
		return textResult("No matching passages found.")
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "[%.3f] %s\n%s\n---\n", r.Score, r.Chunk.Source, strings.TrimSpace(r.Chunk.Text))
	}
	return textResult(strings.TrimSuffix(sb.String(), "\n---\n"))
}
