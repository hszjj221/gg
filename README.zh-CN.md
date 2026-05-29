# gg

[![CI](https://github.com/hszjj221/gg/actions/workflows/ci.yml/badge.svg)](https://github.com/hszjj221/gg/actions/workflows/ci.yml)

[English](README.md) | 简体中文

`gg` 是一个受 Pi 启发的极简 Go coding agent。它以小型 CLI 形式运行，连接 OpenAI-compatible chat completion API，持久化 JSONL 会话，并为模型提供一组紧凑的代码工具。

## Features

- 支持 tool calling 的 OpenAI-compatible streaming provider
- JSONL 会话存储
- 内置代码工具：`read`、`list`、`grep`、`bash`、`edit`、`write`
- 用于聚焦代码库调研的同步只读 `subagent` 工具
- 无第三方 Go 依赖的单二进制 CLI

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

启动简单的按行交互模式：

```bash
gg
```

## Usage

常见示例：

```bash
gg -p "Say hi"
gg --model gpt-4.1 --base-url https://api.openai.com/v1 -p "Read README.md"
gg --no-session -p "Explain this directory"
gg --session .gg/session.jsonl -p "Continue from this file"
```

配置优先级：

- API key：`--api-key`，然后是 `OPENAI_API_KEY`
- Base URL：`--base-url`，然后是 `OPENAI_BASE_URL`，最后是 `https://api.openai.com/v1`
- Model：`--model`，然后是 `GG_MODEL`，最后是 `gpt-4.1`
- Session directory：`--session-dir`，然后是 `GG_SESSION_DIR`，最后是 `~/.gg/sessions`

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

请私下报告安全漏洞。参见 [SECURITY.md](SECURITY.md)。

## License

MIT。参见 [LICENSE](LICENSE)。
