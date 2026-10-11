# Security Policy

## Reporting a Vulnerability

Please do not open a public issue for security vulnerabilities.

Report privately through GitHub security advisories when available. If private advisories are not available, open a minimal issue asking for a private contact path without disclosing exploit details.

Include:

- Affected version or commit
- Reproduction steps
- Expected and actual impact
- Any relevant logs with secrets removed

## Scope

`gg` can execute shell commands and edit files when the model uses the built-in tools. Treat it as a local developer tool and run it only in repositories where you are comfortable granting those capabilities.

Outside a terminal, `--approval auto` (the default, including `gg -p`) denies approval-gated tools such as `bash`, `edit`, and `write`. `--yolo` is the explicit opt-in to run them without a prompt. Dangerous shell commands still need an interactive approval. See the security model in `docs/architecture.md`.
