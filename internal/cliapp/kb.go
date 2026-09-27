package cliapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hszjj221/gg/internal/cli"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/kb"
)

// defaultEmbedModel is used when --embed-model is not given.
const defaultEmbedModel = "text-embedding-3-small"

func runKB(ctx context.Context, cfg config.Config, args cli.Args, stdout, stderr io.Writer) int {
	switch args.KBSub {
	case "index":
		return runKBIndex(ctx, cfg, args, stdout, stderr)
	case "search":
		return runKBSearch(ctx, cfg, args, stdout, stderr)
	case "eval":
		return runKBEval(ctx, cfg, args, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "usage: gg kb <index|search|eval> [options]")
		return 2
	}
}

type embedConfig struct {
	apiKey  string
	baseURL string
	model   string
}

func resolveEmbedConfig(cfg config.Config, args cli.Args) (embedConfig, error) {
	ec := embedConfig{
		apiKey:  firstNonEmpty(args.KBEmbedKey, cfg.APIKey),
		baseURL: firstNonEmpty(args.KBEmbedBase, cfg.BaseURL),
		model:   firstNonEmpty(args.KBEmbedModel, defaultEmbedModel),
	}
	if ec.apiKey == "" {
		return ec, fmt.Errorf("embeddings API key is required: set OPENAI_API_KEY or pass --embed-api-key")
	}
	if ec.baseURL == "" {
		return ec, fmt.Errorf("embeddings base URL is required: set OPENAI_BASE_URL or pass --embed-base-url")
	}
	return ec, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func kbName(args cli.Args) string {
	if args.KBName != "" {
		return args.KBName
	}
	return kb.DefaultName
}

func runKBIndex(ctx context.Context, cfg config.Config, args cli.Args, stdout, stderr io.Writer) int {
	ec, err := resolveEmbedConfig(cfg, args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	name := kbName(args)
	emb := kb.NewOpenAIEmbedder(ec.apiKey, ec.baseURL, ec.model)
	var filesDone, chunksDone int
	ix, err := kb.BuildIndex(ctx, emb, name, args.KBPath, kb.BuildOptions{
		Progress: func(files, chunks int) {
			filesDone, chunksDone = files, chunks
			fmt.Fprintf(stderr, "\rindexing: %d files, %d chunks", files, chunks)
		},
	})
	fmt.Fprintln(stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := ix.Save(cfg.KBDir); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "indexed %d files into %d chunks (model %s, dim %d)\n",
		filesDone, chunksDone, ix.Model, ix.Dim)
	fmt.Fprintf(stdout, "knowledge base %q ready at %s\n", name, kb.IndexPath(cfg.KBDir, name))
	return 0
}

func runKBSearch(ctx context.Context, cfg config.Config, args cli.Args, stdout, stderr io.Writer) int {
	name := kbName(args)
	ix, err := kb.Load(cfg.KBDir, name)
	if err != nil {
		fmt.Fprintf(stderr, "kb_search: %v; build one with `gg kb index <dir>`\n", err)
		return 1
	}
	ec, err := resolveEmbedConfig(cfg, args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// Always embed with the index's own model; mixing models corrupts scores.
	emb := kb.NewOpenAIEmbedder(ec.apiKey, ec.baseURL, ix.Model)
	vecs, err := emb.Embed(ctx, []string{args.KBQuery})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	topK := args.KBTopK
	if topK <= 0 {
		topK = 5
	}
	results := ix.Search(vecs[0], topK)
	if len(results) == 0 {
		fmt.Fprintln(stdout, "no matching passages found")
		return 0
	}
	for _, r := range results {
		fmt.Fprintf(stdout, "[%.3f] %s\n%s\n---\n", r.Score, r.Chunk.Source, strings.TrimSpace(r.Chunk.Text))
	}
	return 0
}

// evalCase is one line of the --cases JSONL file.
type evalCase struct {
	Query  string `json:"query"`
	Expect string `json:"expect"` // substring expected in a top-k hit (case-insensitive)
}

func runKBEval(ctx context.Context, cfg config.Config, args cli.Args, stdout, stderr io.Writer) int {
	name := kbName(args)
	ix, err := kb.Load(cfg.KBDir, name)
	if err != nil {
		fmt.Fprintf(stderr, "kb eval: %v; build one with `gg kb index <dir>`\n", err)
		return 1
	}
	ec, err := resolveEmbedConfig(cfg, args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	cases, err := readEvalCases(args.KBCases)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(cases) == 0 {
		fmt.Fprintln(stderr, "kb eval: no cases in file")
		return 2
	}
	emb := kb.NewOpenAIEmbedder(ec.apiKey, ec.baseURL, ix.Model)
	topK := args.KBTopK
	if topK <= 0 {
		topK = 5
	}
	hits := 0
	for i, c := range cases {
		vecs, err := emb.Embed(ctx, []string{c.Query})
		if err != nil {
			fmt.Fprintf(stderr, "case %d: embed failed: %v\n", i+1, err)
			continue
		}
		results := ix.Search(vecs[0], topK)
		matched := ""
		for _, r := range results {
			if strings.Contains(strings.ToLower(r.Chunk.Text), strings.ToLower(c.Expect)) {
				matched = r.Chunk.Source
				break
			}
		}
		if matched != "" {
			hits++
			fmt.Fprintf(stdout, "PASS  %-60q -> %s\n", c.Query, matched)
		} else {
			top := ""
			if len(results) > 0 {
				top = results[0].Chunk.Source
			}
			fmt.Fprintf(stdout, "FAIL  %-60q (top hit: %s)\n", c.Query, top)
		}
	}
	fmt.Fprintf(stdout, "\nrecall@%d: %d/%d (%.1f%%)\n", topK, hits, len(cases), 100*float64(hits)/float64(len(cases)))
	return 0
}

func readEvalCases(path string) ([]evalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("kb eval: open cases file: %w", err)
	}
	defer f.Close()
	var cases []evalCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		var c evalCase
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, fmt.Errorf("kb eval: line %d: %w", line, err)
		}
		if c.Query == "" || c.Expect == "" {
			return nil, fmt.Errorf("kb eval: line %d: query and expect are required", line)
		}
		cases = append(cases, c)
	}
	return cases, sc.Err()
}
