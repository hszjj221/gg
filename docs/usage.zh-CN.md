# 使用 gg

[English](usage.md) | 简体中文

终端 agent 的命令和配置说明，以及 [README](../README.zh-CN.md) 里点名的扩展。产品概览、架构和安全模型在 README 和[架构文档](architecture.zh-CN.md)里。

<a id="terminal"></a>

## 终端

为 OpenAI-compatible provider 设置 API key：

```bash
export OPENAI_API_KEY=sk-your-key
gg -p "List the files in this project"
gg
```

不带 prompt 的 `gg` 是按行交互模式（Ctrl+D 退出）。回复流到 stdout。provider 送来的思考过程以暗色显示在 stderr，脚本用的 stdout 只保留回答。

```bash
gg -p "Say hi"
gg --model openai:gpt-4.1 --base-url https://api.openai.com/v1 -p "Read README.md"
gg --no-session -p "Explain this directory"
gg --session .gg/session.jsonl -p "Continue from this file"
gg --usage -p "Summarize this repository"
gg --no-skills -p "Run without local skills"
gg --no-memory -p "Run without long-term memory"
gg --approval on-request -p "Run tests and fix failures"
gg --yolo -p "Run tests and fix failures"
gg --sandbox -p "Run the test suite"
gg -p "/skill:ca review and commit my changes"
gg -p "/memory add Prefer concise answers with file references."
gg --name "重构认证模块" -p "Review this module"
```

在终端里，默认的 `--approval auto` 会在 `bash`、`edit`、`write` 运行前询问。不在终端里时，这个模式会拒绝它们，除非传入 `--yolo`。交互命令包括 `/name <名称>`、`/name --clear`、`/compact`、`/context` 和 `/memory`。

<a id="sessions"></a>

## 会话

- `gg sessions list` 列出当前工作目录的会话。
- `gg resume <id-or-path>` 按显示的 id、JSONL 文件名（不含 `.jsonl`）、文件名或路径恢复。
- `gg resume` 和 `gg --resume` 在终端里打开数字编号的选择器。
- `gg --continue` 和 `gg --last` 恢复当前工作目录的最新会话。
- `--name` / `-n` 设置显示名称。
- 会话目录：`--session-dir`，然后是 `GG_SESSION_DIR`，最后是 `~/.gg/sessions`。

Session v3 把消息存在只追加的树里，并用独立的 head 记录保存当前分支。旧会话在打开时升级。用户输入、完整的模型消息和每个工具结果在完成时分别写入。最后一行写坏时，会截回最后一段有效字节。已保存但没有结果的工具调用不会重放；恢复时插入一条结果，说明执行被中断。另一个 `gg` 或 `ggd` 进程已经推进了同一个文件时，过期写入会以冲突失败。

<a id="approval"></a>

## 审批与沙箱

模式有 `auto`（默认）、`on-request`、`yolo` 和 `never`。`--yolo` 选择 `yolo`，不能和另一个不同的 `--approval` 值一起用。`--sandbox` 在 helper 能启动时，于 Linux 上用 bubblewrap、于 macOS 上用 `sandbox-exec` 运行 `bash`。

只读 shell 命令不询问。危险 shell 命令仍然需要终端前有人确认。模式表和分类器见[安全模型](architecture.zh-CN.md#安全模型)。

<a id="providers"></a>

## Provider

存在 `~/.gg/config.json` 时 gg 会读取它：

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

v1 只支持 `openai-compatible`。在 `models` 里列出模型名。没有远端模型发现。

`provider:model` 的选择顺序：`--model`，然后是恢复的会话，然后是配置里的 `default`，最后是 `openai:gpt-4.1`。

API key：`--api-key`，然后是 provider 的 `apiKey`，然后是 `~/.gg/credentials.json`（权限 0600，例如 `{"deepseek": "sk-..."}`），然后是 `apiKeyEnv`。没有配置文件时使用 `OPENAI_API_KEY`。

Base URL：`--base-url`，然后是 provider 的 `baseURL`，然后是按名字内置的 endpoint，然后是 `https://api.openai.com/v1`。内置名字有 `openai`、`deepseek`（`https://api.deepseek.com`）、`openrouter`、`ollama`、`lmstudio`、`moonshot`。显式 `baseURL` 优先。没有配置文件时使用 `OPENAI_BASE_URL`，再不行用 OpenAI 默认值。

推理模型流式输出 `reasoning_content`。gg 把它变成与 provider 无关的 `thinking_delta`，显示在 stderr，并在需要的 provider 上于工具调用续传时回显 `reasoning_content`。思考过程不属于回答正文，也不写入会话记录。

`compat` 让第一次请求就用正确的形状。不设置时，gg 在失败后探测并重试一次：

```json
"o-series": {
  "type": "openai-compatible",
  "baseURL": "https://api.openai.com/v1",
  "apiKeyEnv": "OPENAI_API_KEY",
  "compat": {"completionTokens": true},
  "models": ["o1"]
}
```

- `noStreamUsage`：不发送 `stream_options.include_usage`。
- `completionTokens`：发送 `max_completion_tokens`，不用 `max_tokens`。

网络错误、HTTP 429 和 5xx 在流被接受之前重试。流中断是错误。gg 不会换到另一个 provider 或模型。

<a id="project-instructions"></a>

## 项目规则与编码工具

每一轮都包含编码指令和工作目录。然后 gg 读取 `~/.gg/AGENTS.md`，以及从祖先目录到当前目录的 `AGENTS.md`，每个文件最多 32 KiB，每个用户回合重新读。`--no-context-files` 跳过这些文件，基础指令仍在。

内置编码工具：

- `read` 从指定偏移最多返回 2,000 行或 50 KiB。
- `list` 列出工作目录内的一个目录。
- `grep` 搜索文件内容。`read` 和 `grep` 可以并行；其他工具不行。
- `edit` 同时匹配 LF 和 CRLF，保留未改动的字节和权限，并原子写入。
- `write` 创建或覆盖文件，需要审批。
- `bash` 保留输出的最后 50 KiB。超出时，完整输出保存到 `.gg/outputs/`，并返回路径。在 Unix 上，取消和超时会终止 shell 进程组。

同步的 `subagent` 工具只能用 `read`、`list`、`grep` 做调研。它不能调用 `bash`、`edit`、`write` 或另一个 `subagent`。父 agent 只收到最终总结。子会话的完整记录不会另存为一个会话。

<a id="context"></a>

## 上下文

每次模型请求之前，包括同一任务里连续工具批次之间，gg 按 `context.maxPromptTokens`（默认 24000）估算消息和工具 schema 的大小。打开 `context.autoCompact` 时，先总结旧内容再丢掉。工具调用和结果成批保留。摘要请求也受预算限制。仍然放不下的 prompt 会以错误停止。`context.maxOutputTokens` 默认 4096。`context.summaryMaxTokens` 默认 1200。压缩会追加一条 JSONL `summary` 记录，原始消息留在文件里。token 数是估算。v1 不用某个模型专用的 tokenizer。

<a id="memory"></a>

## 画像与 memory

`gg init` 创建 `~/.gg/USER.md` 和 `~/.gg/memory/`。启动时 gg 注入画像、精选的 `memory/MEMORY.md`，以及今天 `memory/YYYY-MM-DD.md` 的尾部。旧的 `~/.gg/memory.md` 在首次运行时移到 `memory/MEMORY.md`，并保留为 `memory.md.bak`。如果 `MEMORY.md` 已有内容，gg 不会合并；旧文件仍会被改名移开，并打印提示。

`memory.dir` 接受 `~/` 前缀。画像损坏或日常日志删不掉时只打印警告，不阻止启动。

启用 memory 时，模型可以调用 `memory_add`（`general`、`daily`、`person:<name>`、`group:<name>`）和 `memory_search`。`memory_add` 立即写入，不询问审批。交互会话里有 `/memory`、`/memory add`、`/memory show [daily]` 和 `/memory search`。`gg memory search <query>` 和 `gg memory show [daily]` 不用开会话也能用。

`memory.maxPromptTokens` 限制注入的精选记忆量（默认 1200）。文件本身不截断。`memory.dailyLogTailTokens` 默认 500。`memory.dailyLogRetentionDays` 默认 90；`0` 表示保留日常日志。`--no-memory` 或 `memory.enabled=false` 会在该次运行隐藏 memory 工具。

<a id="skills"></a>

## Skills

gg 从 `.agents/skills` 加载 Codex 风格的 skills。当前目录或父目录里的项目 skill 覆盖 `~/.agents/skills` 里的同名 skill。每个 skill 是一个含 `SKILL.md` 的目录：

```markdown
---
name: ca
description: Review local changes and commit after checks pass.
---

# ca
```

提示词里只有名称、描述和路径。模型用 `read` 加载文件。`.agents/skills/.system` 这类隐藏目录会被跳过。`disable-model-invocation: true` 让 skill 不出现在自动列表里；`/skill:<name>` 仍然可以加载。`--no-skills` 关闭这一次运行的发现。

<a id="web-and-electron"></a>

## Web 与 Electron

`ggd` 用 JSON-RPC 暴露会话和运行。Web 客户端走带 Bearer token 的 HTTP。Electron 把同一个 daemon 作为 sidecar 起在 loopback 端口上，每次启动使用随机 token，并通过一个很小的隔离 bridge 把 endpoint 和 token 交给 renderer。两个客户端共享 `ui/src`。

```bash
go install github.com/hszjj221/gg/cmd/ggd@latest
```

Go 版本要求和 `gg` 一样，是 1.26。没有已发布的 release，所以 `@latest` 是默认分支的尖端。在 checkout 里：

```bash
# 终端 1
go run ./cmd/ggd --http 127.0.0.1:8765 --token development-token

# 终端 2
cd ui
npm install
npm run dev:web
```

`ggd` 默认跨工作区最多 16 个并发轮次，每轮截止时间 30 分钟。`--max-active-runs N` 和 `--run-timeout 45m` 调整这两个预算。负值关闭对应限制。默认的 provider HTTP 客户端 5 分钟超时。容量错误可以重试。关闭时取消进行中的工作、等待后台 worker，并在释放实例锁之前关掉 Chromium 和 MCP 子进程。

打开 `http://127.0.0.1:5173`，服务地址留空，输入同一个 token。Vite 把 `/rpc` 和 `/events` 代理到本地 daemon。生产环境应同源提供 `ui/dist`，并把这两条路径代理到 `ggd`。

从源码启动 Electron：

```bash
cd ui
npm install
npm run desktop
```

桌面应用会询问工作区，然后为当前平台构建并启动 `ggd`。签名、安装包和自动更新不在这条命令里。

共享客户端有按日期分组、可搜索的会话，可编辑的任务起点，Markdown 回复，可折叠的工具输出，会话分支，以及文档阅读器。任务运行时，补充要求会作为 steering 发给 runner，在下一个模型边界被读入当前轮。停止会取消这一轮。follow-up 控件把文本放进会话队列，不会注入当前这一轮。

- `Cmd/Ctrl+N` 新建会话。`Cmd/Ctrl+K` 聚焦会话搜索。
- `Enter` 发送消息，或给正在运行的任务补充要求。`Shift+Enter` 换行。`Escape` 关闭导航抽屉或分支面板，并取消尚未完成的会话改名。
- 当前标签页按会话分别保留草稿。重新加载会恢复连接和选中的会话。服务支持时，进行中的运行会重新接上。
- 阅读更早的消息时保持滚动位置。
- 文档支持 Markdown，以及沙箱里的 HTML 预览。完成的版本可以从阅读器存进 library。
- 在桌面应用里，Markdown 中的 HTTP 和 HTTPS 链接用默认浏览器打开。

CLI 不连接 `ggd`。它在进程内跑 agent，所以没有 daemon 也能用。这是同一套 agent 逻辑的第二条执行路径。见[架构文档](architecture.zh-CN.md)。

<a id="scheduler"></a>

## 定时任务

```bash
gg job add --cron "0 9 * * *" --name "morning brief" "Summarize today's calendar"
gg job add --at "2026-09-28 15:04" --name "once" "Remind me"
gg job add --in 20m --name "soon" "Check the build"
```

任务只在 `ggd` 活着时触发（Linux 和 macOS）。`ggd --no-scheduler` 关掉循环。CLI 自己不跑这个循环。每次触发是一个新会话里的一轮 agent，会话名是 `scheduler/<job>-<time>`，可以用 `gg resume` 和 `gg job log` 查看。

无人值守运行会拒绝需要审批的工具。`job add` 时的 `--allow-all` 让该任务选择放行。危险 shell 命令即使这样也会被拒绝。cron 是分钟级的 5 个字段。`--timezone` 是 IANA 时区名（先用画像里的时区，再使用本地时区）。daemon 重启不会补跑错过的 cron。已过期、从未运行过的一次性任务会在启动时触发一次。

任务绑定在创建它的工作区（`gg job show`）。daemon 只触发自己工作区的任务。每个 scheduler 目录只跑一个 `ggd`。

```bash
gg job list [--all]
gg job show|pause|resume|remove <id|name>
gg job log [--job <id|name>] [--limit N]
gg job run <id|name>
```

`gg job remove` 之后，`gg job log` 仍可按 id 读取。状态在 `~/.gg/scheduler/`（`jobs.json`、`runs.jsonl`），有文件锁，文件 0600，目录 0700。触发语义见[架构文档](architecture.zh-CN.md)。

<a id="artifacts-and-library"></a>

## Artifacts 与 library

agent 可以用 `artifact_create` 和 `artifact_edit` 创建带版本的 markdown 或 HTML。文件在 `~/.gg/artifacts/<id>/`（`artifact.json` 加上不可变的 `v1.md`、`v2.md`……），目录 0700，文件 0600。

```bash
gg artifact list
gg artifact show <id>
gg artifact publish <id>
gg artifact remove <id>
gg library add <path> [--name NAME]
gg library list|remove|path <id|name>
```

发布会把最新版本复制进 `~/.gg/library/`。Web UI 渲染 markdown，HTML 放在禁用脚本的沙箱 iframe 里。交互模式下创建或编辑 artifact 需要审批。无人值守运行会拒绝，除非设置了 `--allow-all`。

<a id="connectors"></a>

## Connectors

`gg connect google --client-id ID` 用带 PKCE 的本机 OAuth 回调连接 Gmail 和 Google Calendar。token 存在 `~/.gg/connectors/google.json`（0600）。连接之后，agent 得到 `gmail_search`、`gmail_read`、`gmail_send`、`calendar_agenda` 和 `calendar_create`。发邮件和创建日程需要审批。无人值守运行会拒绝它们，除非设置了 `--allow-all`。

```bash
gg connect list
gg connect status [google]
gg connect remove google
```

客户端配置见 [connectors.zh-CN.md](connectors.zh-CN.md)。

<a id="media"></a>

## 媒体

`gg media image "prompt"`、`gg media tts "text"` 和 `gg media stt <audio-file>` 调用 OpenAI-compatible 的 `/v1/images/generations`、`/v1/audio/speech` 和 `/v1/audio/transcriptions`。默认使用聊天 provider 的 base URL 和 API key。用 `GG_MEDIA_BASE_URL`、`GG_MEDIA_API_KEY` 和 `GG_MEDIA_{IMAGE,TTS,STT}_MODEL` 覆盖。agent 工具是 `image_generate`（需要审批）、`tts` 和 `stt`。文件落在 `~/.gg/media/`。见 [media.zh-CN.md](media.zh-CN.md)。

<a id="browser"></a>

## 浏览器

`gg browser shot <url>` 和 `gg browser read <url>` 通过 CDP 驱动本机 Chromium（`chromium`、`google-chrome` 或 `GG_CHROMIUM`）。只允许 `http` 和 `https`。装了 Chromium 时才会注册 agent 工具 `browser_navigate`、`browser_read` 和 `browser_screenshot`。截图在 `~/.gg/media/screenshots/`。见 [browser.zh-CN.md](browser.zh-CN.md)。

<a id="telegram"></a>

## Telegram

设置 `GG_TELEGRAM_BOT_TOKEN` 和 `GG_TELEGRAM_ALLOW_CHATS`（逗号分隔的 chat id），然后运行 `ggd`。文本和语音按 chat 回复。语音经过 STT、agent，然后是文字回复和 TTS。这个通道上需要审批的工具会被拒绝。见 [telegram.zh-CN.md](telegram.zh-CN.md)。

<a id="knowledge-base"></a>

## 知识库

`gg kb index <dir>` 把文本切块并嵌入 `~/.gg/kb/<name>/index.json`（纯 Go，没有向量数据库）。`gg kb search <query>` 在 CLI 里搜索。默认索引存在之后，agent 获得 `kb_search`。`gg kb eval --cases docs/kb-eval-sample.jsonl` 测量检索 recall@k。见 [rag.zh-CN.md](rag.zh-CN.md)。

<a id="mcp"></a>

## MCP

在 `~/.gg/mcp.json` 里声明服务器（stdio 用 `command` 和 `args`，streamable HTTP 用 `url`）。它们的工具以 `mcp_<server>_<tool>` 暴露。stdio 服务器只继承最小环境。每个 MCP 工具都需要审批。无人值守运行会拒绝它们，除非设置了 `--allow-all`。连接失败的服务器在该次对话的剩余时间里被跳过。见[架构文档](architecture.zh-CN.md#mcp-servers)。

<a id="computer-tools"></a>

## 电脑操作

在 Linux 和 macOS 上，agent 可以调用 `computer_info`、`process_list`、`process_kill`、`open`、`notify`、`clipboard_read` 和 `clipboard_write`。其他平台不广告这个能力。`process_kill`、`open`、`clipboard_read` 和 `clipboard_write` 需要审批。无人值守运行会拒绝它们，除非设置了 `--allow-all`。

<a id="development"></a>

## 开发

```bash
make check
go test -count=1 ./...
go vet ./...
go fmt ./...
```

见 [CONTRIBUTING.md](../CONTRIBUTING.md)。运行诊断见 [diagnostics.zh-CN.md](diagnostics.zh-CN.md)。
