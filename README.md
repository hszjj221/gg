# gg

[![CI](https://github.com/hszjj221/gg/actions/workflows/ci.yml/badge.svg)](https://github.com/hszjj221/gg/actions/workflows/ci.yml)

English | [简体中文](README.zh-CN.md)

`gg` is a minimal Go coding agent inspired by Pi. It runs as a small CLI, talks to OpenAI-compatible chat completion APIs, persists JSONL sessions, and gives the model a compact set of coding tools.

## Features

- OpenAI-compatible streaming provider with tool calling
- Automatic retry for transient model call failures
- JSONL session storage with list and resume commands
- Incremental session persistence and interruption recovery
- Steering and follow-up messages while the agent works
- Project instructions from `AGENTS.md`
- Optional token usage reporting with `--usage`
- Codex-style local skills from `.agents/skills`
- Simple Markdown memory from `~/.gg/memory.md`
- Built-in coding tools: `read`, `list`, `grep`, `bash`, `edit`, `write`
- Synchronous read-only `subagent` tool for focused codebase research
- Single binary Go CLI with a Bubble Tea-powered TUI and inline tool call logs

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
gg --no-memory -p "Run without long-term memory"
gg --approval on-request -p "Run tests and fix failures"
gg -p "/skill:ca review and commit my changes"
gg -p "/memory add Prefer concise answers with file references."
gg sessions list
gg resume <id-or-path> "Continue from this session"
gg --continue "Resume the latest session"
```

Interactive mode:

- Running `gg` in a terminal starts the TUI chat interface.
- The TUI shows the conversation, a single-line prompt input, streaming replies, and a status bar.
- While running, Enter queues a steering message for the next model boundary; Alt+Enter queues a follow-up for after successful completion. Use follow-ups for slash commands.
- Escape or Ctrl+C cancels the active run. Unconsumed queued messages are restored to the input on cancellation or failure.
- Page Up / Page Down scroll history; incoming output preserves your scroll position when reading older messages.
- The TUI also shows compact inline logs for tool calls such as `read`, `bash`, `edit`, `write`, and `subagent`.
- Use `/model` to list configured models and `/model provider:model` to switch the provider/model used by later turns.
- Tool logs are only a TUI view feature; they do not change the JSONL session format or one-shot/line interactive output.
- When stdin/stdout are not terminals, `gg` falls back to the simple line-based interactive mode for scripts and tests.

Tool approval:

- `--approval auto` is the default. TUI sessions ask before running `bash`, `edit`, or `write`; one-shot prompts and non-terminal runs keep the previous non-interactive behavior.
- `--approval on-request` always asks before those tools run and requires a real terminal.
- `--approval never` disables approval prompts.
- Denying a tool call returns a tool error to the model so it can explain or choose another path.

Provider/model configuration:

`gg` reads `~/.gg/config.json` when present:

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

Selection uses `provider:model`:

- Provider/model: `--model provider:model`, then the resumed session model, then config `default`, then `openai:gpt-4.1`
- API key: selected provider `apiKey`; `--api-key` overrides it. If no config file exists, legacy `OPENAI_API_KEY` is used.
- Base URL: selected provider `baseURL`; `--base-url` overrides it. If no config file exists, legacy `OPENAI_BASE_URL` then `https://api.openai.com/v1` are used.
- Session directory: `--session-dir`, then `GG_SESSION_DIR`, then `~/.gg/sessions`
- Memory: `memory.enabled` in config; `--no-memory` disables it for one run.

Only `openai-compatible` providers are supported in v1. Remote model discovery is not implemented; list allowed model names in `models`.

Model calls are retried on temporary failures before streaming, such as network errors, rate limits, and 5xx responses. Interrupted streams, incomplete tool arguments, and output-length limits are reported as errors; partially streamed content is retained and is not blindly retried. Output limits use `max_tokens`, with a compatibility retry for providers explicitly requiring `max_completion_tokens`. `gg` does not fall back to another provider or model; if all retry attempts fail, the normal CLI or TUI error path is used.

Project instructions:

- Every turn includes basic coding instructions and the working directory.
- `gg` reads `~/.gg/AGENTS.md`, then `AGENTS.md` files from ancestor directories to the current directory. Files are re-read for each user turn, limited to 32 KiB each.
- `--no-context-files` disables `AGENTS.md` discovery while retaining the basic instructions.

Tool output and files:

- `read` returns up to 2,000 lines or 50 KiB from the requested offset, including offsets deep inside large files.
- `edit` matches LF and CRLF text consistently, preserves unaffected content and file permissions, and writes atomically.
- `bash` retains the last 50 KiB of output. When truncated, the full output is saved under `.gg/outputs/` and its path is returned for subsequent reads. These logs persist until you remove them.
- On Unix systems, cancellation and timeouts terminate the shell process group; other platforms bound waiting for inherited output pipes.

Session management:

- `gg sessions list` lists sessions for the current working directory.
- `gg resume <id-or-path>` resumes a session by displayed ID, JSONL filename stem, filename, or path.
- `gg --continue` and `gg --last` resume the latest session for the current working directory.
- User messages, completed model messages, and individual tool results are saved as they complete. Provider errors and partial responses remain available after a failed run.
- Resume recovers complete JSONL entries after an interrupted final append. Missing tool results are marked as unknown, so the model can inspect the workspace before retrying.

Context management:

- Before every model request, including requests between tool batches, `gg` checks the estimated message and tool-schema size against `context.maxPromptTokens`.
- With `context.autoCompact` enabled, old content is summarized before removal. Recent history is retained within the available token budget without splitting tool-call/result batches.
- Summary requests are also budgeted and may run in multiple batches. Requests that still exceed the budget stop with an actionable error rather than silently discarding unsummarized content.
- `context.maxOutputTokens` limits ordinary responses (default 4096); `context.summaryMaxTokens` limits summary responses. Configure the input and output budgets to fit your model’s context window.
- Compaction stores a JSONL `summary` entry and keeps recent turns verbatim; original session messages are not deleted or rewritten.
- Resumed sessions use the latest summary plus recent unsummarized turns.
- `/compact` manually writes a new summary, and `/context` shows the current estimated prompt size and budget.
- Token estimation is approximate; v1 does not use a model-specific tokenizer.

Memory:

- `gg` reads `~/.gg/memory.md` by default and injects it as temporary system context for normal prompts.
- Memory is a single Markdown file. It is not written to sessions and is re-read on each turn.
- When memory is enabled, the model can call `memory_add` to append durable preferences or stable facts to `~/.gg/memory.md`.
- `memory_add` writes immediately and does not use the tool approval prompt.
- `/memory` shows memory status, `/memory add <text>` appends a Markdown bullet, and `/memory show` prints the file.
- `memory.maxPromptTokens` limits how much memory is injected; the file itself is never truncated.
- Use `/memory show` or a text editor to review memory. Use `memory.enabled=false` or `--no-memory` to disable memory and hide `memory_add`.

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

The runner exposes `BeforeRequest`, `OnMessage`, and `DrainMessages` hooks for context preparation, durable messages, and steering without coupling the loop to the TUI.

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

`~/.gg/memory.md` is sent to the selected provider when memory is enabled. The model can also append to this file through `memory_add` without approval. Do not ask the agent to remember secrets, tokens, passwords, or sensitive personal data.

Please report vulnerabilities privately. See [SECURITY.md](SECURITY.md).

## License

MIT. See [LICENSE](LICENSE).
