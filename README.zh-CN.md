# gg

[![CI](https://github.com/hszjj221/gg/actions/workflows/ci.yml/badge.svg)](https://github.com/hszjj221/gg/actions/workflows/ci.yml)

[English](README.md) | 简体中文

`gg` 是一款用 Go 编写的开源个人 AI agent——一个 Muse-like 的助手，住在你的终端、浏览器和聊天软件里。它连接 OpenAI-compatible chat completion API，持久化 JSONL 会话，记住你是谁，并给模型提供代码、记忆、定时任务、第三方服务、网页浏览和媒体等方面的工具。

## Features

- 支持 tool calling 的 OpenAI-compatible streaming provider
- 对临时模型调用失败自动重试
- 支持命名、列出、搜索和恢复的 JSONL 会话存储
- 增量保存会话，支持中断恢复
- 运行中补充要求和排队后续任务
- 从 `AGENTS.md` 加载项目规则
- 通过 `--usage` 可选展示 token 消耗
- 从 `.agents/skills` 加载 Codex 风格本地 skills
- 从 `~/.gg/` 加载个人画像与 memory（`gg init` 初始化）
- 内置代码工具：`read`、`list`、`grep`、`bash`、`edit`、`write`
- 用于聚焦代码库调研的同步只读 `subagent` 工具
- 本地 RAG 知识库：`gg kb index` 索引 + agent 可调用的 `kb_search` 工具
- 定时后台任务（`gg job ...`），在 `ggd` daemon 存活时触发
- 可复用的 Go conversation runtime，同一套会话与 Agent 核心可供 CLI、Web 和桌面端使用
- Web 与 Electron 客户端共享一套 React UI，都通过 HTTP 与 `ggd` 通信
- 版本化 artifacts 与个人 library（`gg artifact ...`、`gg library ...`）
- 第三方服务 connectors：Google（Gmail + Calendar）OAuth 接入（`gg connect google`）
- 媒体与语音：图片生成、TTS、STT（`gg media ...`）
- Headless Chromium 浏览（CDP）：导航、读取、截图（`gg browser ...`）
- Telegram channel：`ggd` daemon 按 chat 回复文本和语音消息

## Roadmap

gg 的目标是做一款开源的 Muse-like 个人 agent，工作按阶段推进：

- [x] Phase 1 — 身份与记忆：`~/.gg/USER.md` 个人画像、curated + daily 记忆、`memory_add` / `memory_search`
- [x] Phase 2 — 定时任务：`ggd` daemon 触发的 cron / 一次性后台任务
- [x] Phase 3 — Artifacts 与 library：版本化的 agent 交付物和精选文件库
- [x] Phase 4 — Connectors：OAuth 框架，首批 Google（Gmail + Calendar）
- [x] Phase 5 — 多模态与消息：媒体/TTS/STT、无头浏览、Telegram channel
- [ ] Phase 6 — Goals、Feed、Ideas

## Install

使用 Go 安装最新公开版本：

```bash
go install github.com/hszjj221/gg/cmd/gg@latest
```

也可以从本地 checkout 运行：

```bash
go run ./cmd/gg --help
```

## Quick Start

为 OpenAI-compatible provider 设置 API key：

```bash
export OPENAI_API_KEY=sk-your-key
```

运行一次性 prompt：

```bash
gg -p "List the files in this project"
```

启动交互模式（按行交互，Ctrl+D 退出）：

```bash
gg
```

### Web 与 Electron

新的 `ggd` 进程通过稳定的 JSON-RPC 接口暴露会话和 Agent 能力。Web 端经带 Bearer Token 的 HTTP 使用它；Electron 也一样——在本机以 `--http 127.0.0.1:<端口> --token <随机>` 启动 `ggd` sidecar，renderer 只通过一个最小的隔离 bridge 拿到 endpoint、token 和工作区标签。两个客户端共享一套 React UI 和同一份 HTTP transport。

本地启动 Web 开发环境：

```bash
# 终端 1
go run ./cmd/ggd --http 127.0.0.1:8765 --token development-token

# 终端 2
cd ui
npm install
npm run dev:web
```

打开 `http://127.0.0.1:5173`，服务地址留空并输入同一个 token。Vite 会把 `/rpc` 和 `/events` 转发给本地 `ggd`。生产环境应由同源反向代理提供 `ui/dist`，并将这两个路径转发到 `ggd`。

从源码启动 Electron：

```bash
cd ui
npm install
npm run desktop
```

客户端启动时会让你选择工作目录，然后构建并启动当前平台的本地 `ggd`。当前阶段提供可运行的客户端壳；签名、安装包和自动更新留给后续发布流水线处理。

更完整的分层说明和协议边界见 [架构文档](docs/architecture.zh-CN.md)。

## Usage

常见示例：

```bash
gg -p "Say hi"
gg --model openai:gpt-4.1 --base-url https://api.openai.com/v1 -p "Read README.md"
gg --no-session -p "Explain this directory"
gg --session .gg/session.jsonl -p "Continue from this file"
gg --usage -p "Summarize this repository"
gg --no-skills -p "Run without local skills"
gg --no-memory -p "Run without long-term memory"
gg --approval on-request -p "Run tests and fix failures"
gg -p "/skill:ca review and commit my changes"
gg -p "/memory add Prefer concise answers with file references."
gg sessions list
gg resume <id-or-path> "Continue from this session"
gg --resume
gg --continue "Resume the latest session"
gg --name "重构认证模块" -p "Review this module"
```

交互模式：

- 在终端中运行 `gg` 会启动按行交互模式（Ctrl+D 退出）。
- 回复随生成随输出；`bash`、`edit`、`write` 运行前会行内提示确认。
- 使用 `/name <名称>` 可交互修改会话名，`/name --clear` 清除。
- 想要更丰富的可视化体验（会话树、文档、审批界面），请使用 Web 或桌面客户端。

工具审批：

- `--approval auto` 是默认值。交互模式会在运行 `bash`、`edit` 或 `write` 前询问；一次性 prompt 和非终端运行保持原来的非交互行为。
- `--approval on-request` 会在这些工具运行前始终询问，并要求真实终端。
- `--approval never` 会关闭审批提示。
- 拒绝工具调用时，会把 tool error 返回给模型，模型可以解释原因或选择其他路径。

Provider/model 配置：

`gg` 会在存在时读取 `~/.gg/config.json`：

```json
{
  "default": "openai:gpt-4.1",
  "context": {
    "maxPromptTokens": 24000,
    "maxOutputTokens": 4096,
    "tailTurns": 6,
    "summaryMaxTokens": 1200,
    "autoCompact": true
  },
  "memory": {
    "enabled": true,
    "maxPromptTokens": 1200,
    "dailyLogTailTokens": 500,
    "dailyLogRetentionDays": 90
  },
  "providers": {
    "openai": {
      "type": "openai-compatible",
      "baseURL": "https://api.openai.com/v1",
      "apiKey": "sk-your-key",
      "models": ["gpt-4.1", "gpt-4.1-mini"]
    },
    "local": {
      "type": "openai-compatible",
      "baseURL": "http://localhost:11434/v1",
      "apiKey": "ollama",
      "models": ["qwen2.5-coder"]
    },
    "deepseek": {
      "type": "openai-compatible",
      "apiKeyEnv": "DEEPSEEK_API_KEY",
      "models": ["deepseek-chat", "deepseek-reasoner"]
    }
  }
}
```

`deepseek` 没有写 `baseURL`：gg 会按 provider 名从内置的常见 OpenAI-compatible endpoint 表里推导（`openai`、`deepseek`、`openrouter`、`ollama`、`lmstudio`、`moonshot`）。显式写的 `baseURL` 永远优先；名字不在表里则回退到 `https://api.openai.com/v1`。

模型选择统一使用 `provider:model`：

- Provider/model：`--model provider:model`，然后是恢复会话中的模型，接着是配置里的 `default`，最后是 `openai:gpt-4.1`
- API key：按优先级解析：`--api-key` 参数、当前 provider 的 `apiKey`、`~/.gg/credentials.json`（0600 权限的 JSON，格式如 `{"deepseek": "sk-..."}`）、`apiKeyEnv` 指定的环境变量。`apiKeyEnv` 和 credentials 文件都是为了避免把 secret 写进配置文件。没有配置文件时，沿用 legacy `OPENAI_API_KEY`。
- Base URL：优先级依次为 `--base-url` 参数、当前 provider 的 `baseURL`、按 provider 名推导的常见 endpoint、`https://api.openai.com/v1`。没有配置文件时，沿用 legacy `OPENAI_BASE_URL`，最后是 `https://api.openai.com/v1`。
- Thinking：推理模型（如 `deepseek-reasoner`）会以 `reasoning_content` 流式输出内部思考过程；gg 把它归一化为 provider 无关的 `thinking_delta` 事件。CLI 在交互模式和单次模式下都以暗色显示在 stderr，不污染脚本用的 stdout。gg 会把思考过程保留在 assistant 消息上，在 tool call 续传时按 DeepSeek thinking-mode 工具协议的要求回显 `reasoning_content`。思考过程不会进入回答正文，也不会存入会话记录。

各家 provider 的协议差异写在 `compat` 里。设置后 gg 第一次请求就按正确形状发送；不设置则保持原来的探测行为（失败一次后重试）：

```json
"o-series": {
  "type": "openai-compatible",
  "baseURL": "https://api.openai.com/v1",
  "apiKeyEnv": "OPENAI_API_KEY",
  "compat": {"completionTokens": true},
  "models": ["o1"]
}
```

- `noStreamUsage`：不发送 `stream_options.include_usage`（有些 endpoint 会 400 拒绝这个参数）。
- `completionTokens`：用 `max_completion_tokens` 代替 `max_tokens`。
- Session directory：`--session-dir`，然后是 `GG_SESSION_DIR`，最后是 `~/.gg/sessions`
- Memory：配置里的 `memory.enabled`；`--no-memory` 可单次关闭。

v1 只支持 `openai-compatible` provider。不支持远端拉取模型列表；请在 `models` 里显式列出可选模型。

流式输出开始前遇到网络错误、限流或 5xx 响应等临时失败时会自动重试。流中断、工具参数不完整或输出达到长度限制会明确报错，保留部分回复，不会盲目重试已经输出的内容。输出长度使用 `max_tokens`；服务明确要求 `max_completion_tokens` 时会自动切换参数重试。`gg` 不会 fallback 到其他 provider 或模型；如果所有重试都失败，会继续走现有 CLI 错误展示路径。

项目规则：

- 每轮都会注入基础编码指令和当前工作目录。
- 依次读取 `~/.gg/AGENTS.md`、各级父目录及当前目录的 `AGENTS.md`；每个用户回合重新读取，每个文件上限 32 KiB。
- `--no-context-files` 可关闭 `AGENTS.md` 加载，基础指令仍保留。

工具输出与文件：

- `read` 从指定行开始返回最多 2,000 行或 50 KiB，支持读取大文件深处的内容。
- `edit` 统一匹配 LF / CRLF 文本，保留未修改部分和文件权限，并使用原子写入。
- `bash` 保留输出最后 50 KiB；被截断时，完整日志保存到 `.gg/outputs/`，并返回可继续读取的路径。日志保留到手动删除。
- Unix 系统取消或超时会终止 shell 进程组；其他平台会限制等待继承输出管道的时间。

会话管理：

- `gg sessions list` 会列出当前工作目录的会话。
- `gg resume <id-or-path>` 可以通过显示的 ID、JSONL 文件名（不含 `.jsonl` 后缀）、文件名或路径恢复会话。
- `gg resume` 和 `gg --resume` 会在终端中打开数字编号的会话选择器。
- `gg --continue` 和 `gg --last` 会恢复当前工作目录的最新会话。
- `--name`/`-n` 设置会话显示名称；交互模式使用 `/name <名称>` 修改，使用 `/name --clear` 清除。
- Session v3 会把消息和元数据存入只追加的树结构，并用独立 head 记录持久化当前分支位置；打开旧会话时会自动升级，不丢失历史。
- Web 和桌面客户端提供会话树，支持回退（rewind）、分支（fork）和克隆（clone）操作。
- 用户输入、完整模型消息、每个工具结果分别即时保存；模型调用失败时仍保留已完成操作和部分回复。
- 同一会话被另一个 CLI 或 `ggd` 进程修改时，陈旧写入会返回可重试冲突，不会把两条父链静默交错写入 JSONL。
- 恢复会话时可修复末尾未写完的 JSONL 记录；缺失的工具结果会标记为未知，由模型检查当前状态后再决定是否重试。

上下文管理：

- 每次模型请求前都会检查消息和工具定义的估算大小，包括同一任务中连续工具调用之间的请求。
- 启用 `context.autoCompact` 时，先总结旧内容，再按 token 预算保留近期历史；不会拆开工具调用与结果，也不会直接丢弃未总结的要求。
- 摘要请求也受输入预算限制，必要时分批处理；仍无法满足预算时返回明确错误。
- `context.maxOutputTokens` 限制普通回复长度，默认 4096；`context.summaryMaxTokens` 限制摘要回复长度。输入和输出预算之和应适配所用模型的上下文窗口。
- 压缩会写入 JSONL `summary` entry，并保留最近若干轮原文；原始 session 消息不会被删除或重写。
- 恢复会话时会使用最近 summary 加上尚未压缩的近期 turn。
- `/compact` 可以手动写入新 summary，`/context` 会展示当前估算 prompt 大小和预算。
- token 估算是近似值；v1 不使用具体模型的 tokenizer。

画像与 Memory：

- `gg init` 初始化个人层：`~/.gg/USER.md`（你是谁：名字、时区、语言、备注）和 `~/.gg/memory/`（gg 记住的内容）。
- 启动时 gg 按顺序注入：用户画像、精选记忆（`memory/MEMORY.md`）、今日日志尾部（`memory/YYYY-MM-DD.md`）。
- 老的 `~/.gg/memory.md` 在首次运行时自动迁移到 `memory/MEMORY.md`；原文件保留为 `memory.md.bak`。如果 `MEMORY.md` 已有内容，gg 不会自动合并：原文件同样保留为 `.bak`，并打印一条手动合并提示。
- `memory.dir` 支持 `~/` 前缀（按 home 目录展开）。`~/.gg/USER.md` 损坏或日志删不掉时只打印 warning，不会阻止启动。
- 启用 memory 时，模型可以调用 `memory_add`（`--scope general|daily|person:<name>|group:<name>`）和 `memory_search`（跨 scope 关键词检索）。`memory_add` 会立即写入，并且不走工具审批提示。
- `/memory` 展示 memory 状态，`/memory add [--scope=<scope>] <text>` 追加 Markdown bullet，`/memory show [daily]` 输出内容，`/memory search <query>` 检索。
- `gg memory search <query>` 和 `gg memory show [daily]` 无需启动 session 即可使用。
- `memory.maxPromptTokens` 限制注入的精选记忆大小（原文件不会被截断）；`memory.dailyLogTailTokens` 限制日志尾部；`memory.dailyLogRetentionDays` 清理过期日志（`0` 表示永久保留）。
- 使用 `/memory show` 或文本编辑器检查 memory。使用 `memory.enabled=false` 或 `--no-memory` 可以关闭 memory 并隐藏 memory 工具。

Token 消耗：

- `gg --usage ...` 会在每次运行后把 token 消耗输出到 stderr。
- provider 返回 usage 时，`gg` 会把它记录到 session。
- 不返回 usage 的 provider 仍可使用，并会显示 0 token。

知识库（RAG）：

- `gg kb index <dir>` 把文本文件切分并 embedding，存为本地索引 `~/.gg/kb/<name>/index.json`（纯 Go，无向量数据库）。
- `gg kb search <query>` 在命令行对索引做语义检索。
- 默认知识库建成后，agent 会自动获得 `kb_search` 工具，用于回答关于已索引语料的问题。
- `gg kb eval --cases docs/kb-eval-sample.jsonl` 评测检索 recall@k。架构说明见 `docs/rag.zh-CN.md`。

定时任务：

- `gg job add --cron "0 9 * * *" --name "morning brief" "总结今日日程"` 创建周期任务；`--at "2026-09-28 15:04"` 或 `--in 20m` 创建一次性任务。
- 任务只在 `ggd` daemon 存活时触发（Linux/macOS；`ggd --no-scheduler` 可关闭调度循环）。CLI 本身不跑调度循环。
- 每次触发会开一个全新 session（`scheduler/<job>-<time>`）跑一轮 agent，可用 `gg resume` 审计，记录见 `gg job log`。
- 无人值守运行时，默认拒绝需要审批的工具；`job add` 时加 `--allow-all` 才会放行。只给可信任务开这个选项。
- cron 是 5 字段分钟级；`--timezone` 设置 IANA 时区（默认取用户画像时区，再 fallback 本地）。daemon 重启不会补跑错过的 cron；已过期但从未运行的一次性任务会在启动时补跑一次。
- 每个任务绑定创建时的 workspace（`gg job show` 可查）；daemon 只触发自己 workspace 的任务，在 A 仓库加的任务不会跑到 B 仓库里。每个 scheduler 目录只跑一个 `ggd`。
- `gg job list [--all]`、`gg job show|pause|resume|remove <id|name>`、`gg job log [--job <id|name>] [--limit N]`、`gg job run <id|name>`（立即跑一次，不改变排期）。`gg job remove` 之后仍可用 id 查审计日志。
- 状态存在 `~/.gg/scheduler/`（`jobs.json`、`runs.jsonl`），用文件锁保证 CLI 和 daemon 并发更新不损坏。状态文件仅 owner 可读写（0600），目录 0700，与 session、memory 一致。

Artifacts 与 Library：

- agent 可用 `artifact_create` / `artifact_edit` 工具创建带版本的可交付物（markdown 文档、独立 HTML 页面），存在 `~/.gg/artifacts/<id>/`（`artifact.json` + 不可变版本文件 `v1.md`、`v2.md`…），目录 0700 / 文件 0600。
- `gg artifact list`、`gg artifact show <id>`、`gg artifact publish <id>`、`gg artifact remove <id>`。
- 发布会把最新版本标记为已发布，并拷贝一份到 `~/.gg/library/`（`gg library add <path> [--name NAME]`、`gg library list|remove|path <id|name>`）。Library 是你的文件合集：agent 生成的 + 你自己上传的。
- Web UI 有"文档"页签：markdown 渲染成文档，HTML 在沙箱 iframe 里渲染（不执行脚本）。
- 交互模式下创建/编辑 artifact 需要你确认，和写文件一样；无人值守默认拒绝，除非开了 `--allow-all`。

Connectors（外部服务）：

- `gg connect google --client-id ID` 走 OAuth 流程（localhost 回调 + PKCE）连接 Gmail 和 Google Calendar；token 存 `~/.gg/connectors/google.json`（0600）。client 的创建见 `docs/connectors.zh-CN.md`。
- 连接后 agent 获得 `gmail_search` / `gmail_read` / `gmail_send` 和 `calendar_agenda` / `calendar_create`。发邮件、建日程需要你确认；无人值守默认拒绝，除非开了 `--allow-all`。
- `gg connect list`、`gg connect status [google]`、`gg connect remove google`。

Media & voice（OpenAI 兼容的 `/v1/images/generations`、`/v1/audio/speech`、`/v1/audio/transcriptions`）：

- `gg media image "prompt"`、`gg media tts "text"`、`gg media stt <audio-file>`。默认复用聊天 provider 的 base URL / API key；用 `GG_MEDIA_BASE_URL` / `GG_MEDIA_API_KEY` / `GG_MEDIA_{IMAGE,TTS,STT}_MODEL` 覆盖。详见 `docs/media.zh-CN.md`。
- agent 获得 `image_generate`（需要你确认）、`tts`、`stt` 工具；生成的文件存到 `~/.gg/media/`。

浏览器（headless Chromium 走 CDP，本期只读 + 截图）：

- `gg browser shot <url>`、`gg browser read <url>`。需要本机有 Chromium（`chromium` / `google-chrome`，或 `GG_CHROMIUM`）；只允许 `http(s)` URL。详见 `docs/browser.zh-CN.md`。
- agent 获得 `browser_navigate`、`browser_read`、`browser_screenshot`（只有装了 Chromium 才注册）；截图存到 `~/.gg/media/screenshots/`。

Telegram 频道（daemon）：

- 设置 `GG_TELEGRAM_BOT_TOKEN` 和 `GG_TELEGRAM_ALLOW_CHATS`（逗号分隔的 chat ID），然后运行 `ggd`。文字和语音消息按 chat 分别应答；语音走 STT → agent → 文字 + TTS 回复。需要 approval 的工具在 Telegram 上不可用。详见 `docs/telegram.zh-CN.md`。

MCP 服务器（Model Context Protocol 外部工具）：

- 在 `~/.gg/mcp.json` 中声明服务器（stdio 用 `command`/`args`，streamable HTTP 用 `url`）。每个服务器的工具变成名为 `mcp_<server>_<tool>` 的 agent 工具；stdio 服务器只继承最小环境变量。详见 `docs/architecture.zh-CN.md` 的 MCP 章节。
- 每个 MCP 工具执行前都要你确认；无人值守默认拒绝，除非开了 `--allow-all`。连接失败的服务器在本次会话中会被跳过，不会拖慢每一轮。

电脑操作工具（本地电脑，Linux/macOS）：

- agent 获得 `computer_info`（系统/CPU/内存/磁盘）、`process_list` / `process_kill`、`open`（用默认应用打开文件或 URL）、`notify`（系统通知）、`clipboard_read` / `clipboard_write`。其他平台不提供这一能力。
- `process_kill`、`open`、`clipboard_read`、`clipboard_write` 需要你确认；无人值守默认拒绝，除非开了 `--allow-all`。

## Skills

`gg` 默认从 `.agents/skills` 加载 Codex 风格 skills。当前目录及其父目录中的项目 skills 优先于 `~/.agents/skills` 中的全局 skills。

每个 skill 是一个包含 `SKILL.md` 的目录：

```markdown
---
name: ca
description: Review local changes and commit after checks pass.
---

# ca
```

Skill 行为：

- `gg` 只会把可用 skill 的名称、描述和 `SKILL.md` 路径注入 system prompt。
- 模型可以用 `read` 工具读取 `SKILL.md` 以及该 skill 目录下的文件。
- 会跳过 `.agents/skills/.system` 等隐藏目录。
- 设置了 `disable-model-invocation: true` 的 skill 不会出现在自动 skill 列表中，但仍可以显式加载。
- 使用 `--no-skills` 可以在单次运行中关闭 skill discovery。

强制本轮 prompt 使用某个 skill：

```bash
gg -p "/skill:ca review and commit my changes"
```

## Subagents

`gg` 向模型暴露一个同步 `subagent` 工具。主 agent 可以把聚焦的只读调研任务委派给同进程运行的子 agent。

v1 subagent 有意保持限制：

- 它可以使用 `read`、`list` 和 `grep`。
- 它不能使用 `bash`、`edit`、`write` 或另一个 `subagent`。
- 它只把最终总结返回给主 agent；它的完整 transcript 不会作为独立会话保存。

示例：

```bash
gg -p "Use a subagent to inspect how sessions are stored, then summarize the flow"
```

## Development

Runner 提供 `BeforeRequest`、`OnMessage`、`DrainMessages` 钩子，分别用于准备上下文、持久化消息和交付运行中补充要求，执行循环不依赖任何特定界面。

运行测试套件：

```bash
go test -count=1 ./...
go vet ./...
```

格式化代码：

```bash
go fmt ./...
```

## Security

当模型使用内置工具时，`gg` 可以执行 shell 命令并编辑文件。请只在你愿意授予这些能力的工作区中运行它。

`~/.gg/config.json` 可能包含明文 API key。不要把它提交到仓库，也不要放进不受控的备份。

启用 memory 时，`~/.gg/memory.md` 会发送给当前选中的 provider。模型也可以通过 `memory_add` 不经审批地追加这个文件。不要要求 agent 记住密钥、token、密码或敏感个人信息。

请私下报告安全漏洞。参见 [SECURITY.md](SECURITY.md)。

## License

MIT。参见 [LICENSE](LICENSE)。
