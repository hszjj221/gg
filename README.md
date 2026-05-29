# gg

[![CI](https://github.com/hszjj221/gg/actions/workflows/ci.yml/badge.svg)](https://github.com/hszjj221/gg/actions/workflows/ci.yml)

English | [简体中文](README.zh-CN.md)

`gg` is a minimal Go coding agent inspired by Pi. It runs as a small CLI, talks to OpenAI-compatible chat completion APIs, persists JSONL sessions, and gives the model a compact set of coding tools.

## Features

- OpenAI-compatible streaming provider with tool calling
- JSONL session storage with list and resume commands
- Optional token usage reporting with `--usage`
- Codex-style local skills from `.agents/skills`
- Built-in coding tools: `read`, `list`, `grep`, `bash`, `edit`, `write`
- Synchronous read-only `subagent` tool for focused codebase research
- Single binary Go CLI with no third-party Go dependencies

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

Start the simple line-based interactive mode:

```bash
gg
```

## Usage

Common examples:

```bash
gg -p "Say hi"
gg --model gpt-4.1 --base-url https://api.openai.com/v1 -p "Read README.md"
gg --no-session -p "Explain this directory"
gg --session .gg/session.jsonl -p "Continue from this file"
gg --usage -p "Summarize this repository"
gg --no-skills -p "Run without local skills"
gg -p "/skill:ca review and commit my changes"
gg sessions list
gg resume <id-or-path> "Continue from this session"
gg --continue "Resume the latest session"
```

Configuration precedence:

- API key: `--api-key`, then `OPENAI_API_KEY`
- Base URL: `--base-url`, then `OPENAI_BASE_URL`, then `https://api.openai.com/v1`
- Model: `--model`, then `GG_MODEL`, then `gpt-4.1`
- Session directory: `--session-dir`, then `GG_SESSION_DIR`, then `~/.gg/sessions`

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

Please report vulnerabilities privately. See [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).
