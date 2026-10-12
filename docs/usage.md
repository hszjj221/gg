# Using gg

English | [简体中文](usage.zh-CN.md)

Command and configuration reference for the terminal agent, plus the extensions named from the [README](../README.md). Product overview, architecture, and the security model live there and in [architecture.md](architecture.md).

<a id="terminal"></a>

## Terminal

Set an API key for an OpenAI-compatible provider:

```bash
export OPENAI_API_KEY=sk-your-key
gg -p "List the files in this project"
gg
```

`gg` with no prompt is the line-based interactive mode (Ctrl+D to exit). Replies stream to stdout. Thinking, when the provider sends it, is dimmed on stderr so scripted stdout stays the answer.

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
gg --name "Refactor auth" -p "Review this module"
```

In a terminal, `bash`, `edit`, and `write` ask before they run under the default `--approval auto`. Outside a terminal, that mode denies them unless you pass `--yolo`. Interactive commands include `/name <name>`, `/name --clear`, `/compact`, `/context`, and `/memory`.

<a id="sessions"></a>

## Sessions

- `gg sessions list` lists sessions for the current working directory.
- `gg resume <id-or-path>` resumes by displayed id, JSONL filename stem, filename, or path.
- `gg resume` and `gg --resume` open a numbered picker in the terminal.
- `gg --continue` and `gg --last` resume the latest session for this directory.
- `--name` / `-n` sets the display name.
- Session directory: `--session-dir`, then `GG_SESSION_DIR`, then `~/.gg/sessions`.

Session v3 stores messages in an append-only tree and persists the active branch with a separate head record. Older sessions upgrade on open. User messages, completed model messages, and individual tool results are saved as they finish. A torn final line is truncated back to the last valid bytes. A tool call with no saved result is left unreplayed; resume inserts a result that says the execution was interrupted. If another `gg` or `ggd` process advanced the same file, the stale write fails with a conflict.

<a id="approval"></a>

## Approval and sandbox

Modes are `auto` (default), `on-request`, `yolo`, and `never`. `--yolo` selects `yolo` and cannot be combined with a different `--approval` value. `--sandbox` runs `bash` inside bubblewrap on Linux or `sandbox-exec` on macOS when the helper can start.

Read-only shell commands skip the prompt. Dangerous shell commands still need a person at a terminal. The table and the classifier are in the [security model](architecture.md#security-model).

<a id="providers"></a>

## Providers

`gg` reads `~/.gg/config.json` when it exists:

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

Only `openai-compatible` providers are supported in v1. List model names in `models`. Remote model discovery is not implemented.

Selection order for `provider:model`: `--model`, then the resumed session, then config `default`, then `openai:gpt-4.1`.

API key: `--api-key`, then the provider's `apiKey`, then `~/.gg/credentials.json` (mode 0600, for example `{"deepseek": "sk-..."}`), then `apiKeyEnv`. With no config file, `OPENAI_API_KEY` is used.

Base URL: `--base-url`, then the provider's `baseURL`, then a built-in endpoint for the provider name, then `https://api.openai.com/v1`. Built-in names are `openai`, `deepseek` (`https://api.deepseek.com`), `openrouter`, `ollama`, `lmstudio`, and `moonshot`. An explicit `baseURL` wins. With no config file, `OPENAI_BASE_URL` is used, then the OpenAI default.

Reasoning models stream `reasoning_content`. gg turns that into a provider-neutral `thinking_delta`, shows it on stderr, and echoes `reasoning_content` when continuing a tool call for providers that require it. Thinking is not part of the answer and is not written into the session transcript.

`compat` fixes request shape on the first attempt. Unset, gg probes and retries once after a failed request:

```json
"o-series": {
  "type": "openai-compatible",
  "baseURL": "https://api.openai.com/v1",
  "apiKeyEnv": "OPENAI_API_KEY",
  "compat": {"completionTokens": true},
  "models": ["o1"]
}
```

- `noStreamUsage` omits `stream_options.include_usage`.
- `completionTokens` sends `max_completion_tokens` instead of `max_tokens`.

Network errors, HTTP 429, and 5xx responses retry before a stream is accepted. An interrupted stream is an error. gg does not switch to another provider or model.

<a id="project-instructions"></a>

## Project instructions and coding tools

Every turn includes the coding instructions and the working directory. gg then reads `~/.gg/AGENTS.md` and `AGENTS.md` from ancestor directories down to the current directory, up to 32 KiB each, re-read on each user turn. `--no-context-files` skips those files and keeps the base instructions.

Built-in coding tools:

- `read` returns at most 2,000 lines or 50 KiB from the requested offset.
- `list` lists a directory inside the working directory.
- `grep` searches file contents. `read` and `grep` may run in parallel; other tools do not.
- `edit` matches LF and CRLF, preserves untouched bytes and permissions, and writes atomically.
- `write` creates or replaces a file, subject to approval.
- `bash` keeps the last 50 KiB of output. When that is truncated, the full output is saved under `.gg/outputs/` and the path is returned. On Unix, cancellation and timeouts terminate the shell process group.

A synchronous `subagent` tool can research with `read`, `list`, and `grep` only. It cannot call `bash`, `edit`, `write`, or another `subagent`. The parent receives the final summary. The child transcript is not stored as its own session.

<a id="context"></a>

## Context

Before every model request, including requests between tool batches, gg estimates message and tool-schema size against `context.maxPromptTokens` (default 24000). With `context.autoCompact`, older content is summarized before it is dropped. Tool-call and result batches stay together. Summary requests are budgeted too. A prompt that still does not fit stops with an error. `context.maxOutputTokens` defaults to 4096. `context.summaryMaxTokens` defaults to 1200. Compaction appends a JSONL `summary` entry and leaves the original messages in the file. Token counts are an estimate. v1 does not use a model-specific tokenizer.

<a id="memory"></a>

## Profile and memory

`gg init` creates `~/.gg/USER.md` and `~/.gg/memory/`. On startup gg injects the profile, curated `memory/MEMORY.md`, and the tail of today's `memory/YYYY-MM-DD.md`. A legacy `~/.gg/memory.md` is moved to `memory/MEMORY.md` on first run and kept as `memory.md.bak`. If `MEMORY.md` already has content, gg does not merge it; the legacy file is still renamed aside and a notice is printed.

`memory.dir` accepts a `~/` prefix. A broken profile or an undeletable daily log prints a warning and does not block startup.

When memory is enabled, the model can call `memory_add` (`general`, `daily`, `person:<name>`, `group:<name>`) and `memory_search`. `memory_add` writes immediately and does not ask for approval. `/memory`, `/memory add`, `/memory show [daily]`, and `/memory search` work in the interactive session. `gg memory search <query>` and `gg memory show [daily]` work without starting one.

`memory.maxPromptTokens` limits how much curated memory is injected (default 1200). The file itself is left intact. `memory.dailyLogTailTokens` defaults to 500. `memory.dailyLogRetentionDays` defaults to 90; `0` keeps daily logs. `--no-memory` or `memory.enabled=false` hides the memory tools for that run.

<a id="skills"></a>

## Skills

gg loads Codex-style skills from `.agents/skills`. A project skill in the current directory or a parent overrides the same name in `~/.agents/skills`. Each skill is a directory with `SKILL.md`:

```markdown
---
name: ca
description: Review local changes and commit after checks pass.
---

# ca
```

The prompt receives the name, description, and path. The model loads the file with `read`. Hidden directories such as `.agents/skills/.system` are skipped. `disable-model-invocation: true` hides a skill from the automatic list; `/skill:<name>` still loads it. `--no-skills` disables discovery for one run.

<a id="web-and-electron"></a>

## Web and Electron

`ggd` exposes sessions and runs over JSON-RPC. The Web client uses Bearer-protected HTTP. Electron starts the same daemon as a sidecar on a loopback port, with a per-launch random token, and passes the endpoint and token to the renderer through a small isolated bridge. Both clients share `ui/src`.

```bash
go install github.com/hszjj221/gg/cmd/ggd@latest
```

Same Go 1.26 requirement as `gg`. No release is published, so `@latest` is the default-branch tip. From a checkout:

```bash
# terminal 1
go run ./cmd/ggd --http 127.0.0.1:8765 --token development-token

# terminal 2
cd ui
npm install
npm run dev:web
```

`ggd` defaults to at most 16 concurrent turns across workspaces and a 30-minute deadline per turn. `--max-active-runs N` and `--run-timeout 45m` change those budgets. A negative value disables that limit. The default provider HTTP client times out after 5 minutes. Capacity errors are retryable. Shutdown cancels active work, waits for background workers, and closes Chromium and MCP subprocesses before it releases the instance lock.

Open `http://127.0.0.1:5173`, leave the endpoint blank, and enter the same token. Vite proxies `/rpc` and `/events` to the local daemon. In production, serve `ui/dist` and proxy those paths to `ggd` on the same origin.

Electron from source:

```bash
cd ui
npm install
npm run desktop
```

The desktop app asks for a workspace, then builds and launches `ggd` for the current platform. Signing, installers, and auto-update are not part of this command.

The shared client has searchable sessions grouped by date, editable task starters, Markdown replies, collapsible tool output, conversation branches, and a document reader. While a task runs, adding instructions sends steering that the runner reads at the next model boundary. Stop cancels the run. The follow-up control stores text on the session queue and does not inject it into the current run.

- `Cmd/Ctrl+N` creates a session. `Cmd/Ctrl+K` focuses session search.
- `Enter` sends a message or adds instructions to a running task. `Shift+Enter` inserts a newline. `Escape` closes the navigation drawer or branch panel, and cancels an unfinished session rename.
- Drafts are kept per session in the current tab. Reloading restores the connection and the selected session. Active runs reattach when the server supports it.
- Reading older messages keeps the scroll position.
- Documents support Markdown and sandboxed HTML previews. A completed version can be saved to the library from the reader.
- In the desktop app, HTTP and HTTPS links in Markdown open in the default browser.

The CLI does not talk to `ggd`. It runs the agent in-process so it works with no daemon. That is a second execution path for the same agent logic. See [architecture.md](architecture.md).

<a id="scheduler"></a>

## Scheduler

```bash
gg job add --cron "0 9 * * *" --name "morning brief" "Summarize today's calendar"
gg job add --at "2026-09-28 15:04" --name "once" "Remind me"
gg job add --in 20m --name "soon" "Check the build"
```

Jobs fire only while `ggd` is alive (Linux and macOS). `ggd --no-scheduler` disables the loop. The CLI does not run it. Each firing is one agent turn in a new session named `scheduler/<job>-<time>`, visible with `gg resume` and `gg job log`.

Unattended runs deny approval-gated tools. `--allow-all` on `job add` opts that job in. Dangerous shell commands are denied even then. Cron is five fields at minute resolution. `--timezone` is an IANA name (profile timezone, then local). A daemon restart does not catch up missed cron firings. A past-due one-shot that never ran fires once on startup.

A job is bound to the workspace where it was created (`gg job show`). A daemon fires jobs for its own workspace. Run one `ggd` per scheduler directory.

```bash
gg job list [--all]
gg job show|pause|resume|remove <id|name>
gg job log [--job <id|name>] [--limit N]
gg job run <id|name>
```

`gg job log` stays readable by id after `gg job remove`. State is `~/.gg/scheduler/` (`jobs.json`, `runs.jsonl`), file-locked, mode 0600 files and 0700 directory. Firing semantics are in [architecture.md](architecture.md).

<a id="artifacts-and-library"></a>

## Artifacts and library

The agent can create versioned markdown or HTML with `artifact_create` and `artifact_edit`. Files live in `~/.gg/artifacts/<id>/` (`artifact.json` plus immutable `v1.md`, `v2.md`, …), directory 0700, files 0600.

```bash
gg artifact list
gg artifact show <id>
gg artifact publish <id>
gg artifact remove <id>
gg library add <path> [--name NAME]
gg library list|remove|path <id|name>
```

Publishing copies the latest version into `~/.gg/library/`. The Web UI renders markdown, and HTML inside a sandboxed iframe with scripts disabled. Creating or editing an artifact needs approval in interactive mode. Unattended runs deny it unless `--allow-all` is set.

<a id="connectors"></a>

## Connectors

`gg connect google --client-id ID` runs a localhost OAuth callback with PKCE for Gmail and Google Calendar. Tokens are stored in `~/.gg/connectors/google.json` (0600). After connecting, the agent gets `gmail_search`, `gmail_read`, `gmail_send`, `calendar_agenda`, and `calendar_create`. Sending mail and creating events need approval. Unattended runs deny them unless `--allow-all` is set.

```bash
gg connect list
gg connect status [google]
gg connect remove google
```

Client setup: [connectors.md](connectors.md).

<a id="media"></a>

## Media

`gg media image "prompt"`, `gg media tts "text"`, and `gg media stt <audio-file>` call OpenAI-compatible `/v1/images/generations`, `/v1/audio/speech`, and `/v1/audio/transcriptions`. They default to the chat provider's base URL and API key. Override with `GG_MEDIA_BASE_URL`, `GG_MEDIA_API_KEY`, and `GG_MEDIA_{IMAGE,TTS,STT}_MODEL`. The agent tools are `image_generate` (approval required), `tts`, and `stt`. Files land in `~/.gg/media/`. See [media.md](media.md).

<a id="browser"></a>

## Browser

`gg browser shot <url>` and `gg browser read <url>` drive a local Chromium (`chromium`, `google-chrome`, or `GG_CHROMIUM`) over CDP. Only `http` and `https` URLs are allowed. The agent tools `browser_navigate`, `browser_read`, and `browser_screenshot` are registered when Chromium is installed. Screenshots go to `~/.gg/media/screenshots/`. See [browser.md](browser.md).

<a id="telegram"></a>

## Telegram

Set `GG_TELEGRAM_BOT_TOKEN` and `GG_TELEGRAM_ALLOW_CHATS` (comma-separated chat ids), then run `ggd`. Text and voice are answered per chat. Voice goes through STT, the agent, then a text reply and TTS. Approval-gated tools are denied on this channel. See [telegram.md](telegram.md).

<a id="knowledge-base"></a>

## Knowledge base

`gg kb index <dir>` chunks and embeds text into `~/.gg/kb/<name>/index.json` (pure Go, no vector database). `gg kb search <query>` searches from the CLI. Once the default index exists, the agent gains `kb_search`. `gg kb eval --cases docs/kb-eval-sample.jsonl` measures retrieval recall@k. See [rag.md](rag.md).

<a id="mcp"></a>

## MCP

Declare servers in `~/.gg/mcp.json` (`command` and `args` for stdio, `url` for streamable HTTP). Their tools are exposed as `mcp_<server>_<tool>`. stdio servers inherit a minimal environment. Every MCP tool needs approval. Unattended runs deny them unless `--allow-all` is set. A server that fails to connect is skipped for the rest of that conversation. See [architecture.md](architecture.md#mcp-servers).

<a id="computer-tools"></a>

## Computer tools

On Linux and macOS the agent can call `computer_info`, `process_list`, `process_kill`, `open`, `notify`, `clipboard_read`, and `clipboard_write`. The capability is not advertised on other platforms. `process_kill`, `open`, `clipboard_read`, and `clipboard_write` need approval. Unattended runs deny them unless `--allow-all` is set.

<a id="development"></a>

## Development

```bash
make check
go test -count=1 ./...
go vet ./...
go fmt ./...
```

See [CONTRIBUTING.md](../CONTRIBUTING.md). Run diagnostics: [diagnostics.md](diagnostics.md).
