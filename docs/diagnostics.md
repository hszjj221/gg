# Run diagnostics and performance

English | [简体中文](diagnostics.zh-CN.md)

CLI and daemon share JSON diagnostics on stderr. Enable detailed records with
`GG_LOG_LEVEL=debug`; accepted levels are `debug`, `info` (default), `warn`
(`warning` is an alias), and `error`.

```bash
GG_LOG_LEVEL=debug gg -p "Inspect the project" 2>gg-debug.jsonl
GG_LOG_LEVEL=debug ggd --http 127.0.0.1:8765 --token development-token 2>ggd-debug.jsonl
```

At the default level, daemon logs run start/completion and operational warnings.
Debug enables conversation, model request, provider attempt, approval and tool
timings. CLI stdout continues to carry its normal result. Embedding hosts may
supply `app.Options.Log` / `cliapp.Options.Log` to choose their own handler.

## Follow a run

| Fields | Meaning |
| --- | --- |
| `sessionID`, `runID` | Associate execution records with the session and transport run. In-process CLI calls generate their own run ID; persistence-disabled calls have an empty session ID. |
| `requestID`, `requestKind`, `model` | Monotonic request number within the run, including shared-provider subagent requests; `turn` or `compaction`. |
| `tool`, `toolCallID` | Associate preparation, approval and execution with a tool call. |
| `durationMs`, `outcome` | Elapsed time and success, failure, cancellation or timeout. Preparation can be ready/skipped; approval can be denied. |
| `attempts`, `retries`, `compatibilityRetries` | Provider invocations, actual transient retries and protocol compatibility fallbacks, counted separately. A canceled backoff can have a scheduled retry that never executes. |
| `httpStatus`, `errorType`, `errorCode`, `retryable` | API failure status (0 when no API error), Go error type and stable application error classification. Detailed errors still return to the caller. |
| `promptTokens`, `completionTokens`, `totalTokens` | Usage returned by requests/tools and aggregate run usage, including compaction. |

Normal diagnostic records omit prompts, reasoning, tool arguments, tool results,
API keys and raw provider error bodies. Existing operational warnings and panic
reports retain their detailed errors/stacks. Terminal diagnostics are emitted
before completion becomes visible to run waiters, outside the manager lock.

## Reproduce the performance baseline

```bash
go test -run '^$' -bench 'Benchmark(Snapshot|TreeProjection|ContextBuild)$' -benchmem -benchtime=150ms -count=3 ./internal/app ./internal/session ./internal/contextmgr
go test -run '^$' -bench '^BenchmarkSnapshot/records_10000$' -benchtime=600ms -cpuprofile /tmp/snapshot.cpu -memprofile /tmp/snapshot.heap -o /tmp/gg-app.test ./internal/app
go tool pprof -top -sample_index=alloc_space /tmp/gg-app.test /tmp/snapshot.heap
```

Fixtures contain 100, 1,000 or 10,000 linear records with 256-byte text. The
compacted-context fixture retains 20 messages and one summary. Fixture loading
and file writes are excluded from benchmark timing. These measure core
projections and context construction; they exclude JSON transport serialization,
network/model latency, rendering and production branch distributions.

Local medians of three runs, Apple M4 / darwin arm64 / Go 1.27.1, 10,000 records:

| Operation | Before | After | Bytes/op before → after |
| --- | --- | --- | --- |
| Session snapshot | 9.75 ms | 3.95 ms | 41,202,864 → 10,477,225 |
| Tree projection | 5.20 ms | 3.51 ms | 12,987,787 → 7,908,779 |
| Full-history context | 2.04 ms | 2.02 ms | 3,695,104 → 3,695,104 |
| Compacted context | 71.2 µs | 4.21 µs | 1,691,712 → 8,256 |

Allocation profiling identified `Store.State`/`populateLoaded` as the main
snapshot cost. Snapshot and session updates now read active-branch identity
without reconstructing the full loaded history. Tree projection reads validated
ancestry directly instead of cloning it. Context capacity is based on retained
history, so compacted prefixes no longer inflate its allocation. No projection
cache or persistent format change is involved. Full-history context cost remains
linear, and snapshots still include the full visible tree.

The local toolchain lacked the `pprof` executable; the allocation sample was
read with its bundled Go profile parser. The generated profiles remain usable
with `go tool pprof` on a toolchain that includes it.
