# Personal knowledge base (RAG)

`gg` ships a minimal, dependency-free retrieval layer for the user's own
documents: a personal knowledge base the agent can semantically search via
the `kb_search` tool. No vector database, no extra services — one JSON file
under `~/.gg/kb/<name>/index.json`. The index itself stays on the user's
machine; note that during indexing, document text is sent to the configured
embeddings provider (only the resulting vectors are stored locally).

It complements long-term memory rather than duplicating it:

| | Memory (`memory_search`) | Knowledge base (`kb_search`) |
|---|---|---|
| Content | Distilled facts about the user: preferences, people, daily logs | Text of the user's documents, notes, manuals, saved material (capped at 512 KiB per file) |
| Size | Small, curated | Large, un-distilled |
| Prompt | `MEMORY.md` injected (budgeted); people, groups, and older daily logs searched on demand | Searched on demand, never injected wholesale |
| Query | Keyword | Semantic (embeddings) |

Rule of thumb for the agent: "what does the user like / who is X / what did
we discuss" → `memory_search`. "What does the user's document say about Y" →
`kb_search`. "What does this code do" → `read` / `grep`, not the KB.

## Pipeline

```
gg kb index <dir>            gg kb search <query> / kb_search tool
┌──────────────┐              ┌──────────────┐
│ walk <dir>   │              │ embed query  │
│  skip .git,  │              │  (index's    │
│  node_modules│              │   own model)  │
└──────┬───────┘              └──────┬───────┘
       ▼                             ▼
┌──────────────┐              ┌──────────────┐
│ chunk text   │              │ cosine search│
│  paragraphs, │              │  brute force │
│  overlap     │              │  top-k       │
└──────┬───────┘              └──────┬───────┘
       ▼                             ▼
┌──────────────┐              ┌──────────────┐
│ embed chunks │              │ return chunk │
│  batched     │              │  text+source │
│  /embeddings │              │  +score      │
└──────┬───────┘              └──────────────┘
       ▼
┌──────────────┐
│ save index   │
│  index.json  │
└──────────────┘
```

## Components

| Piece | Location | Notes |
| --- | --- | --- |
| Chunking | `internal/kb/chunk.go` | Paragraph split on blank lines, code fences kept intact, ~1200 chars/chunk, 150-char overlap. Oversized paragraphs are hard-split on UTF-8 boundaries. |
| Embeddings | `internal/kb/embed.go` | `Embedder` interface; `OpenAIEmbedder` posts to any OpenAI-compatible `/embeddings` endpoint, 64 texts per request. |
| Index build | `internal/kb/build.go` | Walks the tree, skips VCS/dependency/build dirs, allowlists text extensions, probes unknown files for binary content, caps files at 512 KiB. |
| Vector store | `internal/kb/store.go` | JSON-persisted chunks + vectors with a manifest (name, model, dim, timestamp). Atomic write via temp file + rename. |
| Search | `internal/kb/store.go` | Brute-force cosine similarity. Exact, dependency-free, fast enough for tens of thousands of chunks. |
| Agent tool | `internal/tools/kb_search.go` | `kb_search(query, top_k)`; registered only when the default index exists, so the tool list stays clean. The tool description steers the agent: user documents → `kb_search`, distilled user facts → `memory_search`, live code → `read`/`grep`. Embeds the query with the **index's own model** to avoid silent dimension/model mismatch. |
| CLI | `internal/cliapp/kb.go` | `gg kb index|search|eval`. |

## Key design decisions

- **Query embedded with the index's model, not a flag.** The manifest records
  the embedding model; both the tool and `kb search` reuse it. Mixing models
  silently corrupts cosine scores, so this is enforced, not suggested.
- **Brute-force search, deliberately.** At documentation scale (thousands of
  chunks) exact cosine over float32 vectors is sub-millisecond to milliseconds.
  An ANN index (HNSW) can replace `Index.Search` later without changing the
  file format or the tool contract.
- **Overlap between chunks.** A concept split across a chunk boundary is still
  retrievable because consecutive chunks share ~150 characters.
- **Read-only at query time.** The tool never touches the network except for
  the single embedding call; the index is a local file.
- **No index, no tool.** `kb_search` is registered only when
  `~/.gg/kb/default/index.json` exists, so users without a knowledge base see
  the same tool list as before.
- **Query endpoint must match the build endpoint.** The manifest records which
  embeddings endpoint built the index. If the agent/CLI is later configured
  with a different endpoint, `kb_search` / `kb search` refuse to run (fail
  closed) instead of querying the wrong service or ranking with incompatible
  vectors. Query vectors are also rejected when their dimension does not match
  the index.
- **Private on disk.** The index contains full document text, so the KB
  directory is created `0700` and `index.json` `0600`.
- **UTF-8 sanitized on ingest.** Files are normalized to valid UTF-8 when
  read, so a corrupt source file can neither hang the chunker nor break the
  embeddings request payload.

## Usage

```bash
# Index your own documents: notes, manuals, saved articles (plain-text formats)
# (needs an embeddings-capable API key)
export OPENAI_API_KEY=<key>          # or pass --embed-api-key
gg kb index ~/Documents/notes        # -> ~/.gg/kb/default/index.json
gg kb index ~/Documents/manuals --name manuals --embed-model text-embedding-3-large

# Dedicated embeddings endpoint (when the chat provider doesn't serve embeddings):
# flags --embed-base-url / --embed-api-key, or env GG_EMBED_BASE_URL / GG_EMBED_API_KEY.
# The same endpoint must be configured wherever you query (agent or CLI);
# a mismatch fails closed with instructions instead of silently misbehaving.
export GG_EMBED_BASE_URL=https://embed.example.com/v1
export GG_EMBED_API_KEY=<key>
gg kb index ./docs

# Query manually
gg kb search "what did my Tokyo trip notes say about hotels?" --top-k 3

# In the agent: the kb_search tool appears automatically once the
# default knowledge base exists.

# Evaluate retrieval quality
gg kb eval --cases docs/kb-eval-sample.jsonl --top-k 5
```

Cases file format (JSONL, `#` comments allowed):

```json
{"query": "which hotel did I book in Tokyo?", "expect": "Park Hyatt"}
```

A case passes when any top-k hit contains `expect` (case-insensitive).
The report prints per-case PASS/FAIL plus `recall@k`.

## Trade-offs and future work

- Chunking is paragraph-based, not semantic or AST-aware; code-heavy corpora
  may benefit from symbol-aware splitting later.
- No reranker: top-k cosine is the final ranking. A cross-encoder rerank step
  is the obvious next quality lever.
- No incremental updates: re-indexing rebuilds from scratch. Fine for docs;
  large corpora will want change detection.
- Only the `default` knowledge base is exposed to the agent; named bases are
  CLI-only for now.
