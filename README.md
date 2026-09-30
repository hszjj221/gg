# gg

[![CI](https://github.com/hszjj221/gg/actions/workflows/ci.yml/badge.svg)](https://github.com/hszjj221/gg/actions/workflows/ci.yml)

English | [简体中文](README.zh-CN.md)

`gg` is a minimal Go coding agent inspired by Pi. It runs as a small CLI, talks to OpenAI-compatible chat completion APIs, persists JSONL sessions, and gives the model a compact set of coding tools.

## Features

- OpenAI-compatible streaming provider with tool calling
- Automatic retry for transient model call failures
- Named JSONL sessions with list, search, and resume flows
- Incremental session persistence and interruption recovery
- Steering and follow-up messages while the agent works
- Project instructions from `AGENTS.md`
- Optional token usage reporting with `--usage`
- Codex-style local skills from `.agents/skills`
- Personal profile and memory from `~/.gg/` (`gg init` scaffolds it)
- Built-in coding tools: `read`, `list`, `grep`, `bash`, `edit`, `write`
- Synchronous read-only `subagent` tool for focused codebase research
- Local RAG knowledge base: `gg kb index` + agent-callable `kb_search` tool
- Scheduled background jobs (`gg job ...`) that fire while the `ggd` daemon is alive
- Reusable Go conversation runtime shared by the CLI, Web, and desktop clients
- Web and Electron clients built from one React UI, both talking to `ggd` over HTTP

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

Start the interactive mode (line-based, Ctrl+D to exit):

```bash
gg
```

### Web and Electron

The new `ggd` process exposes sessions and agent runs through a stable JSON-RPC interface. The Web client reaches it over Bearer-protected HTTP; Electron starts it as a local sidecar the same way — over HTTP on a loopback port with a per-launch random Bearer <redacted> handed to the renderer through a minimal isolated bridge. Both clients share one React UI and one HTTP transport.

Start the local Web development environment:

```bash
# terminal 1
go run ./cmd/ggd --http 127.0.0.1:8765 --token development-token

# terminal 2
cd ui
npm install
npm run dev:web
```

Open `http://127.0.0.1:5173`, leave the endpoint blank, and enter the same token. Vite proxies `/rpc` and `/events` to the local daemon. In production, serve `ui/dist` and proxy those paths to `ggd` from the same origin.

Run Electron from source:

```bash
cd ui
npm install
npm run desktop
```

The desktop app asks for a workspace, then builds and launches the local `ggd` binary for the current platform. This stage provides the runnable desktop shell; signing, installers, and auto-update belong in a later release pipeline.

See the [architecture guide](docs/architecture.md) for the dependency boundaries and protocol.

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
gg --resume
gg --continue "Resume the latest session"
gg --name "Refactor auth" -p "Review this module"
```

Interactive mode:

- Running `gg` in a terminal starts the line-based interactive mode (Ctrl+D to exit).
- Replies stream as they arrive; tool approvals prompt inline before `bash`, `edit`, or `write` run.
- Use `/name <name>` to rename the session interactively, `/name --clear` to remove the name.
- For a richer visual experience (conversation tree, artifacts, approvals UI), use the Web or desktop client.

Tool approval:

- `--approval auto` is the default. Interactive sessions ask before running `bash`, `edit`, or `write`; one-shot prompts and non-terminal runs keep the previous non-interactive behavior.
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

`deepseek` omits `baseURL`: gg infers it from a small table of well-known OpenAI-compatible endpoints (`openai`, `deepseek`, `openrouter`, `ollama`, `lmstudio`, `moonshot`). An explicit `baseURL` always wins; unknown names fall back to `https://api.openai.com/v1`.

Selection uses `provider:model`:

- Provider/model: `--model provider:model`, then the resumed session model, then config `default`, then `openai:gpt-4.1`
- API key: `--api-key` flag first, then the selected provider's `apiKey`, then the `apiKeyEnv` environment variable. `apiKeyEnv` names an env var holding the key so secrets don't have to live in the config file. If no config file exists, legacy `OPENAI_API_KEY` is used.
- Base URL: `--base-url` flag first, then the selected provider's `baseURL`, then the well-known endpoint for the provider name, then `https://api.openai.com/v1`. If no config file exists, legacy `OPENAI_BASE_URL` then `https://api.openai.com/v1` are used.

Provider quirks go in `compat`. When set, gg sends the right request shape on the first attempt; when unset, it probes and retries once after a failed request, as before:

```json
"o-series": {
  "type": "openai-compatible",
  "baseURL": "https://api.openai.com/v1",
  "apiKeyEnv": "OPENAI_API_KEY",
  "compat": {"completionTokens": true},
  "models": ["o1"]
}
```

- `noStreamUsage`: omit `stream_options.include_usage` (some endpoints reject it with a 400).
- `completionTokens`: send `max_completion_tokens` instead of `max_tokens`.
- Session directory: `--session-dir`, then `GG_SESSION_DIR`, then `~/.gg/sessions`
- Memory: `memory.enabled` in config; `--no-memory` disables it for one run.

Only `openai-compatible` providers are supported in v1. Remote model discovery is not implemented; list allowed model names in `models`.

Model calls are retried on temporary failures before streaming, such as network errors, rate limits, and 5xx responses. Interrupted streams, incomplete tool arguments, and output-length limits are reported as errors; partially streamed content is retained and is not blindly retried. Output limits use `max_tokens`, with a compatibility retry for providers explicitly requiring `max_completion_tokens`. `gg` does not fall back to another provider or model; if all retry attempts fail, the normal CLI error path is used.

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
- `gg resume` and `gg --resume` open a numbered session picker in the terminal.
- `gg --continue` and `gg --last` resume the latest session for the current working directory.
- `--name`/`-n` sets a session display name; `/name <name>` changes it interactively and `/name --clear` removes it.
- Session v3 stores messages and metadata in an append-only tree and persists the active branch with a separate head record. Older sessions upgrade without discarding history.
- The Web and desktop clients expose the conversation tree with rewind, branch (fork), and clone actions.
- User messages, completed model messages, and individual tool results are saved as they complete. Provider errors and partial responses remain available after a failed run.
- If another CLI or `ggd` process advances the same session, stale writes fail with a retryable conflict instead of silently interleaving two parent chains in the JSONL file.
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

Profile and memory:

- `gg init` scaffolds the personal layer: `~/.gg/USER.md` (who you are: name, timezone, language, notes) and `~/.gg/memory/` (what gg remembers).
- On startup gg injects, in order: user profile, curated memory (`memory/MEMORY.md`), and today's daily-log tail (`memory/YYYY-MM-DD.md`).
- A legacy `~/.gg/memory.md` is migrated to `memory/MEMORY.md` automatically on first run; the legacy file is kept as `memory.md.bak`. If `MEMORY.md` already has content, gg does not merge automatically: the legacy file is still kept as `.bak` and a manual-merge notice is printed.
- `memory.dir` accepts a `~/` prefix (expanded against your home directory). A broken `~/.gg/USER.md` or an undeletable daily log only prints a warning and never blocks startup.
- When memory is enabled, the model can call `memory_add` with `--scope general|daily|person:<name>|group:<name>` and `memory_search` for keyword search across all scopes. `memory_add` writes immediately and does not use the tool approval prompt.
- `/memory` shows status, `/memory add [--scope=<scope>] <text>` appends a bullet, `/memory show [daily]` prints, `/memory search <query>` searches.
- `gg memory search <query>` and `gg memory show [daily]` work without starting a session.
- `memory.maxPromptTokens` limits curated memory injection (the file itself is never truncated); `memory.dailyLogTailTokens` limits the daily-log tail; `memory.dailyLogRetentionDays` prunes old daily logs (`0` keeps them forever).
- Use `/memory show` or a text editor to review memory. Use `memory.enabled=false` or `--no-memory` to disable memory and hide the memory tools.

Token usage:

- `gg --usage ...` prints token usage to stderr after each run.
- Usage is recorded in the session when the provider returns it.
- Providers that do not return usage remain supported and report zero tokens.

Knowledge base (RAG):

- `gg kb index <dir>` chunks and embeds text files into a local index at `~/.gg/kb/<name>/index.json` (pure Go, no vector database).
- `gg kb search <query>` runs semantic search over the index from the CLI.
- Once the default knowledge base exists, the agent gains a `kb_search` tool for answering questions about the ingested corpus.
- `gg kb eval --cases docs/kb-eval-sample.jsonl` measures retrieval recall@k. See `docs/rag.md` for the architecture.

Scheduled jobs:

- `gg job add --cron "0 9 * * *" --name "morning brief" "Summarize today's calendar"` creates a recurring job; `--at "2026-09-28 15:04"` or `--in 20m` creates a one-shot job.
- Jobs only fire while the `ggd` daemon is alive (Linux/macOS; `ggd --no-scheduler` disables the loop). The CLI never runs the scheduling loop itself.
- Each firing runs one agent turn in a fresh session named `scheduler/<job>-<time>`, auditable with `gg resume` and recorded in `gg job log`.
- Unattended runs deny tools that need approval by default; pass `--allow-all` at `job add` time to let the job use them. Only enable it for jobs you trust.
- Cron uses the 5-field minute-level format; `--timezone` sets the IANA timezone (default: your profile timezone, then local). Restarting the daemon does not catch up missed cron firings; a past-due one-shot job that never ran fires once on startup.
- Each job is bound to the workspace it was created from (`gg job show` displays it); a daemon only fires jobs for its own workspace, so a job added in repo A never runs in repo B. Run a single `ggd` per scheduler directory.
- `gg job list [--all]`, `gg job show|pause|resume|remove <id|name>`, `gg job log [--job <id|name>] [--limit N]`, and `gg job run <id|name>` (fire once now without changing the schedule). The run log stays readable by id even after `gg job remove`.
- State lives in `~/.gg/scheduler/` (`jobs.json`, `runs.jsonl`), guarded by a file lock so the CLI and daemon can update it concurrently. State files are owner-only (0600) and the directory is 0700, like sessions and memory.

Artifacts and library:

- The agent can create versioned deliverables with the `artifact_create` / `artifact_edit` tools (markdown documents and standalone HTML pages). They live in `~/.gg/artifacts/<id>/` (`artifact.json` plus immutable `v1.md`, `v2.md`, …), directory 0700 / files 0600.
- `gg artifact list`, `gg artifact show <id>`, `gg artifact publish <id>`, `gg artifact remove <id>`.
- Publishing marks the latest version published and saves a copy into your library at `~/.gg/library/` (`gg library add <path> [--name NAME]`, `gg library list|remove|path <id|name>`). The library is your curated file collection: agent-generated files plus files you upload.
- The Web UI has an Artifacts tab: markdown renders as a document, HTML renders inside a sandboxed iframe (no scripts execute).
- Creating or editing an artifact needs your approval in interactive mode, like file writes; unattended runs deny it unless `--allow-all` is set.

Connectors (third-party services):

- `gg connect google --client-id ID` runs an OAuth flow (localhost callback + PKCE) for Gmail and Google Calendar; tokens live in `~/.gg/connectors/google.json` (0600). See `docs/connectors.md` for the client setup.
- Once connected, the agent gets `gmail_search` / `gmail_read` / `gmail_send` and `calendar_agenda` / `calendar_create`. Sending mail and creating events need your approval; unattended runs deny them unless `--allow-all` is set.
- `gg connect list`, `gg connect status [google]`, `gg connect remove google`.

Media & voice (OpenAI-compatible `/v1/images/generations`, `/v1/audio/speech`, `/v1/audio/transcriptions`):

- `gg media image "prompt"`, `gg media tts "text"`, `gg media stt <audio-file>`. Defaults to the chat provider's base URL / API key; override with `GG_MEDIA_BASE_URL` / `GG_MEDIA_API_KEY` / `GG_MEDIA_{IMAGE,TTS,STT}_MODEL`. See `docs/media.md`.
- The agent gets `image_generate` (needs approval), `tts`, and `stt` tools; generated files land in `~/.gg/media/`.

Browser (headless Chromium over CDP, read + screenshot in this phase):

- `gg browser shot <url>`, `gg browser read <url>`. Needs a local Chromium binary (`chromium` / `google-chrome`, or `GG_CHROMIUM`); only `http(s)` URLs are allowed. See `docs/browser.md`.
- The agent gets `browser_navigate`, `browser_read`, `browser_screenshot` (only registered when Chromium is installed); screenshots land in `~/.gg/media/screenshots/`.

Telegram channel (daemon):

- Set `GG_TELEGRAM_BOT_TOKEN` and `GG_TELEGRAM_ALLOW_CHATS` (comma-separated chat IDs), then run `ggd`. Text and voice messages are answered per-chat; voice goes through STT → agent → text + TTS reply. Approval-gated tools are denied over Telegram. See `docs/telegram.md`.

MCP servers (external tools via the Model Context Protocol):

- Declare servers in `~/.gg/mcp.json` (`command`/`args` for stdio, `url` for streamable HTTP). Each server's tools become agent tools named `mcp_<server>_<tool>`; stdio servers inherit only a minimal environment. See the MCP section in `docs/architecture.md`.
- Every MCP tool needs your approval before it runs; unattended runs deny them unless `--allow-all` is set. A server that fails to connect is skipped for the rest of the conversation, never fatal.

Computer tools (local machine operation, Linux/macOS):

- The agent gets `computer_info` (OS/arch/CPU/memory/disk), `process_list` / `process_kill`, `open` (open files or URLs with the default application), `notify` (system notification), and `clipboard_read` / `clipboard_write`. The capability is not advertised on other platforms.
- `process_kill`, `open`, `clipboard_read`, and `clipboard_write` need your approval; unattended runs deny them unless `--allow-all` is set.

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

The runner exposes `BeforeRequest`, `OnMessage`, and `DrainMessages` hooks for context preparation, durable messages, and steering without coupling the loop to any particular interface.

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
