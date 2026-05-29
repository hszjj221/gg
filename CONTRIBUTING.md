# Contributing

Thanks for helping improve `gg`.

## Development Setup

Requirements:

- Go 1.26 or newer
- An OpenAI-compatible API key for manual smoke tests

Run the local checks before sending a pull request:

```bash
go test -count=1 ./...
go vet ./...
go run ./cmd/gg --help
```

## Pull Requests

Keep pull requests small and focused. Include tests for behavior changes, and update `README.md` when user-facing behavior changes.

For code style, use standard Go formatting:

```bash
go fmt ./...
```

## Security

Do not report security vulnerabilities in public issues. See `SECURITY.md` for private reporting guidance.
