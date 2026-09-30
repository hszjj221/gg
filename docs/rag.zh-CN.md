# 个人知识库（RAG）

`gg` 自带一个最小、无依赖的检索层，服务于用户自己的文档：agent 可以通过 `kb_search` 工具对个人知识库做语义检索。不用向量数据库，不用额外服务——就是 `~/.gg/kb/<name>/index.json` 一个 JSON 文件。索引留在用户本机；注意索引构建时文档文本会发给配置的 embeddings provider（本地只存生成的向量）。

它和长期记忆是互补关系，不是重复：

| | 记忆（`memory_search`） | 知识库（`kb_search`） |
|---|---|---|
| 内容 | 关于用户的提炼事实：偏好、人物、日常日志 | 用户文档、笔记、手册、收藏资料的原文（单文件上限 512 KiB） |
| 体量 | 小而精 | 大而全 |
| 注入 | `MEMORY.md` 注入（按预算）；人物、群组和旧日志按需检索 | 按需检索，绝不整库注入 |
| 查询 | 关键词 | 语义（embeddings） |

agent 的经验法则："用户喜欢什么 / X 是谁 / 我们之前聊过什么" → `memory_search`。"用户的文档里 Y 是怎么说的" → `kb_search`。"这段代码是干什么的" → `read` / `grep`，别走知识库。

## 流水线

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

## 组件

| 组件 | 位置 | 说明 |
| --- | --- | --- |
| 分块 | `internal/kb/chunk.go` | 按空行切段落，代码围栏保持完整，约 1200 字符一块，重叠 150 字符。超长段落在 UTF-8 边界硬切。 |
| Embeddings | `internal/kb/embed.go` | `Embedder` 接口；`OpenAIEmbedder` 调任意 OpenAI 兼容的 `/embeddings` 端点，每次请求 64 条文本。 |
| 索引构建 | `internal/kb/build.go` | 遍历目录树，跳过 VCS/依赖/构建目录，只收文本扩展名，未知文件先探测是否为二进制，单文件上限 512 KiB。 |
| 向量存储 | `internal/kb/store.go` | JSON 持久化 chunks + vectors，带 manifest（name、model、dim、timestamp）。临时文件 + rename 原子写。 |
| 检索 | `internal/kb/store.go` | 暴力余弦相似度。精确、无依赖，几万个 chunk 也够快。 |
| Agent 工具 | `internal/tools/kb_search.go` | `kb_search(query, top_k)`；只有默认索引存在时才注册，工具列表保持干净。工具描述会引导 agent：用户文档 → `kb_search`，提炼过的用户事实 → `memory_search`，实时代码 → `read`/`grep`。用**索引自己的模型** embed 查询，避免悄无声息的维度/模型错配。 |
| CLI | `internal/cliapp/kb.go` | `gg kb index|search|eval`。 |

## 关键设计决策

- **查询用索引的模型 embed，不用 flag 指定。** manifest 记录 embedding 模型；工具和 `kb search` 都复用它。混用模型会悄无声息地污染余弦分数，所以这里是强制，不是建议。
- **暴力检索是故意的。** 文档量级（几千个 chunk）下，float32 向量的精确余弦是亚毫秒到毫秒级。以后可以用 ANN 索引（HNSW）替换 `Index.Search`，文件格式和工具契约都不用变。
- **chunk 之间重叠。** 一个概念被切在 chunk 边界上时，相邻 chunk 共享约 150 字符，照样能检回来。
- **查询时只读。** 除了 embed 查询的那一次网络调用，工具不碰网络；索引就是本地文件。
- **没有索引就没有工具。** 只有 `~/.gg/kb/default/index.json` 存在时才注册 `kb_search`，没建知识库的用户看到的工具列表和以前一样。
- **查询端点必须和构建端点一致。** manifest 记录构建索引用的 embeddings 端点。如果 agent/CLI 后来配了别的端点，`kb_search` / `kb search` 会拒绝运行（fail closed），而不是去查错的服务或用不兼容的向量排序。维度对不上的查询向量同样会被拒绝。
- **磁盘上是私有的。** 索引里有文档全文，所以 KB 目录建 0700，`index.json` 建 0600。
- **入库时做 UTF-8 清洗。** 读取时把文件规范成合法 UTF-8，坏文件既卡不住分块器，也破坏不了 embeddings 请求体。

## 用法

```bash
# 索引你自己的文档：笔记、手册、收藏的文章（纯文本格式）
# （需要支持 embeddings 的 API key）
export OPENAI_API_KEY=<key>          # 或传 --embed-api-key
gg kb index ~/Documents/notes        # -> ~/.gg/kb/default/index.json
gg kb index ~/Documents/manuals --name manuals --embed-model text-embedding-3-large

# 独立的 embeddings 端点（聊天 provider 不提供 embeddings 时）：
# flag --embed-base-url / --embed-api-key，或环境变量 GG_EMBED_BASE_URL / GG_EMBED_API_KEY。
# 查询（agent 或 CLI）必须配同一个端点；
# 端点不一致会 fail closed 并给出指引，而不是悄悄出错。
export GG_EMBED_BASE_URL=https://embed.example.com/v1
export GG_EMBED_API_KEY=<key>
gg kb index ./docs

# 手动查询
gg kb search "what did my Tokyo trip notes say about hotels?" --top-k 3

# 在 agent 里：默认知识库建好后 kb_search 工具自动出现。

# 评测检索质量
gg kb eval --cases docs/kb-eval-sample.jsonl --top-k 5
```

Cases 文件格式（JSONL，`#` 注释允许）：

```json
{"query": "which hotel did I book in Tokyo?", "expect": "Park Hyatt"}
```

一条 case 只要 top-k 里有命中包含 `expect`（大小写不敏感）就算过。报告打印每条 PASS/FAIL 和 `recall@k`。

## 取舍与后续工作

- 分块按段落，不感知语义也不感知 AST；代码多的语料以后可能需要按符号切。
- 没有 reranker：top-k 余弦就是最终排序。加 cross-encoder 重排是显而易见的下一个质量抓手。
- 没有增量更新：重建索引就是全量重建。文档量级够用；大语料以后要做变更检测。
- agent 只看得到 `default` 知识库；命名知识库目前只能走 CLI。
