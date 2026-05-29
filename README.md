# gg

[![CI](https://github.com/hszjj221/gg/actions/workflows/ci.yml/badge.svg)](https://github.com/hszjj221/gg/actions/workflows/ci.yml)

English | [简体中文](README.zh-CN.md)

`gg` is a minimal Go coding agent inspired by Pi. It runs as a small CLI, talks to OpenAI-compatible chat completion APIs, persists JSONL sessions, and gives the model a compact set of coding tools.

## Features

- OpenAI-compatible streaming provider with tool calling
- Automatic retry for transient model call failures
- JSONL session storage with list and resume commands
- Optional token usage reporting with `--usage`
- Codex-style local skills from `.agents/skills`
- Built-in coding tools: `read`, `list`, `grep`, `bash`, `edit`, `write`
- Synchronous read-only `subagent` tool for focused codebase research
- Single binary Go CLI with a Bubble Tea-powered TUI

## Install

Install the latest public version with Go:

```bash
go install github.com/hszjj221/gg/cmd/gg@latest
```

Or run from a local checkout:

```bash
go run ./cmd/gg --help
```

## Quick Start

Set an API key for an OpenAI-compatible provider:

```bash
export OPENAI_API_KEY=sk-your-key
```

Run a one-shot prompt:

```bash
gg -p "List the files in this project"
```

Start the TUI interactive mode:

```bash
gg
```

## Usage

Common examples:

```bash
gg -p "Say hi"
gg --model openai:gpt-4.1 --base-url https://api.openai.com/v1 -p "Read README.md"
gg --no-session -p "Explain this directory"
gg --session .gg/session.jsonl -p "Continue from this file"
gg --usage -p "Summarize this repository"
gg --no-skills -p "Run without local skills"
gg -p "/skill:ca review and commit my changes"
gg sessions list
gg resume <id-or-path> "Continue from this session"
gg --continue "Resume the latest session"
```

Interactive mode:

- Running `gg` in a terminal starts the TUI chat interface.
- The TUI shows the conversation, a single-line prompt input, streaming replies, and a status bar.
- Use `/model` to list configured models and `/model provider:model` to switch the provider/model used by later turns.
- When stdin/stdout are not terminals, `gg` falls back to the simple line-based interactive mode for scripts and tests.

Provider/model configuration:

`gg` reads `~/.gg/config.json` when present:

```json
{
  "default": "openai:gpt-4.1",
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

Selection uses `provider:model`:

- Provider/model: `--model provider:model`, then the resumed session model, then config `default`, then `openai:gpt-4.1`
- API key: selected provider `apiKey`; `--api-key` overrides it. If no config file exists, legacy `OPENAI_API_KEY` is used.
- Base URL: selected provider `baseURL`; `--base-url` overrides it. If no config file exists, legacy `OPENAI_BASE_URL` then `https://api.openai.com/v1` are used.
- Session directory: `--session-dir`, then `GG_SESSION_DIR`, then `~/.gg/sessions`

Only `openai-compatible` providers are supported in v1. Remote model discovery is not implemented; list allowed model names in `models`.

Model calls are retried on temporary failures such as network errors, rate limits, and 5xx responses. `gg` does not fall back to another provider or model; if all retry attempts fail, the normal CLI or TUI error path is used.

Session management:

- `gg sessions list` lists sessions for the current working directory.
- `gg resume <id-or-path>` resumes a session by displayed ID, JSONL filename stem, filename, or path.
- `gg --continue` and `gg --last` resume the latest session for the current working directory.

Token usage:

- `gg --usage ...` prints token usage to stderr after each run.
- Usage is recorded in the session when the provider returns it.
- Providers that do not return usage remain supported and report zero tokens.

## Skills

`gg` loads Codex-style skills from `.agents/skills` by default. Project skills in the current directory or its parents take precedence over global skills in `~/.agents/skills`.

Each skill is a directory with a `SKILL.md` file:

```markdown
---
name: ca
description: Review local changes and commit after checks pass.
---

# ca
```

Skill behavior:

- `gg` injects only the available skill name, description, and `SKILL.md` location into the system prompt.
- The model can use the `read` tool to load `SKILL.md` and files under that skill directory.
- Hidden directories such as `.agents/skills/.system` are skipped.
- Skills with `disable-model-invocation: true` are hidden from the automatic skill list, but can still be loaded explicitly.
- Use `--no-skills` to disable skill discovery for a run.

Force a skill for one prompt:

```bash
gg -p "/skill:ca review and commit my changes"
```

## Subagents

`gg` exposes a synchronous `subagent` tool to the model. The main agent can delegate focused read-only research tasks to a child agent running in the same process.

The v1 subagent is intentionally limited:

- It can use `read`, `list`, and `grep`.
- It cannot use `bash`, `edit`, `write`, or another `subagent`.
- It returns only its final summary to the main agent; its full transcript is not stored as a separate session.

Example:

```bash
gg -p "Use a subagent to inspect how sessions are stored, then summarize the flow"
```

## Development

Run the test suite:

```bash
go test -count=1 ./...
go vet ./...
```

Format code:

```bash
go fmt ./...
```

## Security

`gg` can execute shell commands and edit files when the model uses the built-in tools. Run it only in workspaces where you are comfortable granting those capabilities.

`~/.gg/config.json` may contain cleartext API keys. Keep it out of repositories and backups you do not control.

Please report vulnerabilities privately. See [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).
