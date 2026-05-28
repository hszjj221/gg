# gg

`gg` is a minimal Go coding agent inspired by Pi. It provides a small CLI, an OpenAI-compatible streaming provider, JSONL sessions, and four built-in coding tools: `read`, `bash`, `edit`, and `write`.

## Usage

```bash
export OPENAI_API_KEY=sk-...
go run ./cmd/gg -p "List the files in this project"
```

Common options:

```bash
gg -p "Say hi"
gg --model gpt-4.1 --base-url https://api.openai.com/v1 -p "Read README.md"
gg --no-session -p "Explain this directory"
gg --session .gg/session.jsonl -p "Continue from this file"
```

With no prompt, `gg` starts a simple line-based interactive mode.

## Configuration

Resolution order:

- API key: `--api-key`, then `OPENAI_API_KEY`
- Base URL: `--base-url`, then `OPENAI_BASE_URL`, then `https://api.openai.com/v1`
- Model: `--model`, then `GG_MODEL`, then `gpt-4.1`
- Session directory: `--session-dir`, then `GG_SESSION_DIR`, then `~/.gg/sessions`

## Development

```bash
go test ./...
go run ./cmd/gg --help
```
