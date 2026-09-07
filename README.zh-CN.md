# gg

[![CI](https://github.com/hszjj221/gg/actions/workflows/ci.yml/badge.svg)](https://github.com/hszjj221/gg/actions/workflows/ci.yml)

[English](README.md) | 简体中文

`gg` 是一个受 Pi 启发的极简 Go coding agent。它以小型 CLI 形式运行，连接 OpenAI-compatible chat completion API，持久化 JSONL 会话，并为模型提供一组紧凑的代码工具。

## Features

- 支持 tool calling 的 OpenAI-compatible streaming provider
- 对临时模型调用失败自动重试
- 支持列出和恢复命令的 JSONL 会话存储
- 增量保存会话，支持中断恢复
- 运行中补充要求和排队后续任务
- 从 `AGENTS.md` 加载项目规则
- 通过 `--usage` 可选展示 token 消耗
- 从 `.agents/skills` 加载 Codex 风格本地 skills
- 从 `~/.gg/memory.md` 加载简单 Markdown memory
- 内置代码工具：`read`、`list`、`grep`、`bash`、`edit`、`write`
- 用于聚焦代码库调研的同步只读 `subagent` 工具
- 带 Bubble Tea TUI 和内联工具调用日志的单二进制 Go CLI

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

启动 TUI 交互模式：

```bash
gg
```

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
gg --continue "Resume the latest session"
```

交互模式：

- 在终端中运行 `gg` 会启动 TUI chat 界面。
- TUI 会展示对话、单行 prompt 输入框、streaming 回复和状态栏。
- 运行中按 Enter 可补充要求，在下一次模型调用前交付；Alt+Enter 可排队后续任务，当前任务成功后执行。斜杠命令请使用后续任务队列。
- Escape 或 Ctrl+C 取消当前执行；取消或失败后，尚未交付的排队消息会恢复到输入框。
- Page Up / Page Down 可翻阅历史，流式输出不会打断对旧消息的阅读。
- TUI 还会以内联紧凑日志展示 `read`、`bash`、`edit`、`write`、`subagent` 等工具调用。
- 使用 `/model` 查看已配置模型，使用 `/model provider:model` 切换后续 turn 使用的 provider/model。
- 工具日志只是 TUI 视图能力；不会改变 JSONL session 格式，也不会影响一次性 prompt 或按行交互输出。
- 当 stdin/stdout 不是终端时，`gg` 会回退到简单的按行交互模式，方便脚本和测试使用。

工具审批：

- `--approval auto` 是默认值。TUI 会在运行 `bash`、`edit` 或 `write` 前询问；一次性 prompt 和非终端运行保持原来的非交互行为。
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
    "maxPromptTokens": 1200
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
    }
  }
}
```

模型选择统一使用 `provider:model`：

- Provider/model：`--model provider:model`，然后是恢复会话中的模型，接着是配置里的 `default`，最后是 `openai:gpt-4.1`
- API key：当前 provider 的 `apiKey`；`--api-key` 可以覆盖它。没有配置文件时，沿用 legacy `OPENAI_API_KEY`。
- Base URL：当前 provider 的 `baseURL`；`--base-url` 可以覆盖它。没有配置文件时，沿用 legacy `OPENAI_BASE_URL`，最后是 `https://api.openai.com/v1`。
- Session directory：`--session-dir`，然后是 `GG_SESSION_DIR`，最后是 `~/.gg/sessions`
- Memory：配置里的 `memory.enabled`；`--no-memory` 可单次关闭。

v1 只支持 `openai-compatible` provider。不支持远端拉取模型列表；请在 `models` 里显式列出可选模型。

流式输出开始前遇到网络错误、限流或 5xx 响应等临时失败时会自动重试。流中断、工具参数不完整或输出达到长度限制会明确报错，保留部分回复，不会盲目重试已经输出的内容。输出长度使用 `max_tokens`；服务明确要求 `max_completion_tokens` 时会自动切换参数重试。`gg` 不会 fallback 到其他 provider 或模型；如果所有重试都失败，会继续走现有 CLI 或 TUI 错误展示路径。

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
- `gg --continue` 和 `gg --last` 会恢复当前工作目录的最新会话。
- 用户输入、完整模型消息、每个工具结果分别即时保存；模型调用失败时仍保留已完成操作和部分回复。
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

Memory：

- `gg` 默认读取 `~/.gg/memory.md`，并把它作为临时 system context 注入普通 prompt。
- Memory 是单个 Markdown 文件，不会写入 session，并且每个 turn 都会重新读取。
- 启用 memory 时，模型可以调用 `memory_add` 把长期偏好或稳定事实追加到 `~/.gg/memory.md`。
- `memory_add` 会立即写入，并且不走工具审批提示。
- `/memory` 展示 memory 状态，`/memory add <text>` 会追加 Markdown bullet，`/memory show` 会输出文件内容。
- `memory.maxPromptTokens` 限制注入的 memory 大小；原文件不会被截断。
- 使用 `/memory show` 或文本编辑器检查 memory。使用 `memory.enabled=false` 或 `--no-memory` 可以关闭 memory 并隐藏 `memory_add`。

Token 消耗：

- `gg --usage ...` 会在每次运行后把 token 消耗输出到 stderr。
- provider 返回 usage 时，`gg` 会把它记录到 session。
- 不返回 usage 的 provider 仍可使用，并会显示 0 token。

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

Runner 提供 `BeforeRequest`、`OnMessage`、`DrainMessages` 钩子，分别用于准备上下文、持久化消息和交付运行中补充要求，执行循环不依赖 TUI。

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
