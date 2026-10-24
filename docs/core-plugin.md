# Core + first-party plugins

[中文](core-plugin.zh-CN.md)

This records the approved Core+plugin plan: the C0 architecture decision (based on `main@932e14e`), C1 Registry and Tool/Connector SPI (based on `main@e77769a`), and C2 profile enable lists (based on `main@53fb4be`). C0 changed documentation only. C1 established the in-tree SPI with Google as the statically linked pilot. C2 filters provider construction and capability reporting by profile while preserving coding tools, defaults, and approval policy. The [architecture overview](architecture.md) describes the running system; C3 and later phases below remain future work.

## Goal and boundaries

Keep one Go Core with a stable service provider interface (SPI). First-party plugins remain in-tree Go packages, compiled into the same binary and assembled by Core. A profile selects an ordered enable list and defaults within that runtime. Third-party capabilities remain MCP-first, using the existing external-process or HTTP boundary.

The purpose is to make capability ownership and assembly explicit without weakening the coding product. A plugin boundary does not require a separate process, a separately installed package, or a smaller binary. Static linking gives first-party capabilities the same build, review, and release lifecycle as Core; C2 profile filtering reduces the enabled capability surface, not the linked binary size.

First-party implementation status and later candidates are:

- **Connectors**: Google is the C1 pilot, registered through the SPI; OAuth/API code remains in `internal/connector/google`.
- **Browser** and **media** capability implementations.
- **Telegram channel** and **proactive delivery policy** implementations.
- **Artifact/library**, optionally later if reducing binary size becomes a concrete requirement. An enable list alone cannot remove their linked code.

C0 listed candidates; C1 keeps the SPI in `app` to avoid moving shared host dependencies. Neither phase authorizes a bulk directory move.

## Core invariants

These responsibilities never leave Core, even when the implementation making a request belongs to a plugin:

| Responsibility | Core ownership |
| --- | --- |
| Agent loop and session | Turn execution, cancellation, steering, session identity, persistence, branching, concurrency, run results, and event replay. |
| JSON-RPC and SSE | Method and DTO contracts, validation, capability reporting, approval events, reconnect/replay semantics, and transport access checks. A channel or plugin consumes these contracts; it cannot replace them. |
| Approval pipeline | Core owns the execution gate, risk routing, human decisions, unattended denial, and audit. Plugin operations use this path rather than executing around it. |
| Standing authorization | Grant storage, explicit provenance, matching, expiry, revocation, and allow audit. Enabling a plugin or profile never creates a grant. |
| Consequence attributes | Shared meanings and routing for E/M/U and `reversible`, including unknown values. Implementations describe effects; Core applies policy. |
| Configuration and assembly | Config resolution and validation, daemon composition and supervision, registry assembly, profile parsing, enable-list resolution, and coding defaults. |
| Memory and user profile | Storage contracts, scope/path rules, permissions, migrations, and shared prompt integration for memory and `USER.md`. Plugins use these contracts rather than inventing competing stores. |

The existing consequence contract in [`agent.ApprovalRequest`](../internal/agent/types.go) remains authoritative:

| Attribute | Meaning |
| --- | --- |
| E / `externallyVisible` | Effects or traces visible outside the local process, including to a service operator. |
| M / `spendsMoneyOrChangesPermissions` | Spending beyond normal chat, or changing authority/access. |
| U / `actsInUserName` | Creating or sending something attributable to the user, including in a private account. |
| R / `reversible` | The whole effect can be undone cheaply and locally without an external party noticing. R is independent of E/M/U. |

An omitted consequence is unknown, not evidence of safety or authorization. Asserted E/M/U still needs a human decision or a matching explicit standing grant, including under coding `--yolo` and scheduled `--allow-all`. Standing grants do not waive `dangerous`/`confirm` risks. R alone does not authorize an operation. C0 neither expands classification coverage nor removes the existing risk-based compatibility path for unclassified calls; see the [personal-agent consequence and approval inventory](personal-agent.md).

First-party plugins execute with gg's OS privileges. Static linking and an SPI provide ownership boundaries, not a sandbox. Implementations must preserve existing approval descriptions and cannot bypass approval or standing-auth. Proactive decisions about when to surface a digest are delivery policy, not authorization to send, notify, or write memory. MCP tools retain their existing universal approval gate and documented classification limitations.

## Lessons from Magpie and dsh

This table records the comparison used in the approved plan. It guides gg's ownership decisions, without adopting the other projects' runtime or distribution model.

| Dimension | Magpie | dsh | gg choice |
| --- | --- | --- | --- |
| Host language | Go + Bun plugins | Node/Cordis | Keep Go Core. |
| Plugin form | External npm/JS | npm + patch layers | In-tree Go first; external capabilities via MCP. |
| Assembly | Install/migrate ownership | Profile bundle stacks | Profile enable list + defaults. |
| Trust | Host forces protocol | Version gates | Core forces approval/standing-auth/E·M·U and preserves reversible metadata. |
| Market | Community plugins | Plugin manager UI | C4+ consideration only; no market now. |

**Magpie lesson:** make Core versus plugin ownership and package lifecycle explicit. A capability needs an owner for registration, initialization, configuration, resource cleanup, compatibility, and any data migration. gg can adopt that discipline while shipping first-party code in the existing Go binary.

**dsh lesson:** a profile is an ordered composition of capability layers, expressed as enable lists and defaults. It is not a second runtime. Core still resolves the composition, runs the agent, and enforces authorization.

## SPI and package lifecycle

C1 defines the source-level SPI in [`app/plugin.go`](../internal/app/plugin.go), retaining `ToolProvider` and `ToolContext` under `app` as the smaller boundary. `NewRegistry` snapshots providers in registration order; C2 adds `Registry.ForProfile` to resolve the enabled subsequence before construction or capability reporting. `Registry.Build` applies this selection from `ToolContext.Config.Profile`; `ToolCapabilities(profile)` reports the selected registry's `Capabilities`. Both paths retain `Available` platform gates. Empty names remain unadvertised core providers. `Options.ToolProviders` still supports host/test injection: nil selects the built-ins, while a non-nil empty slice disables all tools, including in forked conversations. Profile filtering also applies to host-injected named providers.

`ConnectorProvider` names a token-backed connector and builds its tools from the host context, existing store, and current token. `ConnectorTools` registers these providers under the existing `connector` capability. Missing tokens remain expected absence; store/token failures propagate to the existing degradation reporting. Tool provider failures still discard that provider's tools for the turn, while successful builds clear previous failures. [`conversationTools`](../internal/app/toolset.go) retains logging, sanitized degradation reporting, and per-conversation cleanup; discovery and execution use the same Registry. Registration remains trusted Go composition, with no new metadata validation or version-negotiation protocol. The ownership contract is:

| Lifecycle area | Core / host | First-party implementation |
| --- | --- | --- |
| Registration and compatibility | Own identifiers, deterministic assembly, validation, and the SPI compatibility contract. | Register capability metadata and implementations through the agreed SPI. |
| Configuration and selection | Parse profiles/config and resolve enabled capabilities and defaults. | Consume validated capability configuration; preserve existing connection/scopes behavior. |
| Build and discovery | Use one selected registry for tool definitions, execution, and advertised capabilities. Preserve platform gates and expected-disabled versus degraded reporting. | Build tools/clients and report availability or operational failure through that contract. |
| Execution and trust | Keep agent dispatch, approvals, standing-auth, consequence interpretation, and audit. | Describe operation effects and execute only through Core's dispatch and approval path. |
| Resource lifetime | Own session/daemon lifetimes and call cleanup on shutdown or retirement. | Release owned clients, connections, and other resources through the host lifecycle. |
| Persistence and migration | Retain shared storage contracts and coordinate compatible startup/migrations. | Own capability-specific data and migration logic within that lifecycle; preserve existing paths and permissions during migration. |

The [`Google pilot registration`](../internal/app/googleplugin.go) now uses this Connector SPI. It preserves scope-based tool selection and the existing `gmail_search`, `gmail_read`, `gmail_send`, `calendar_agenda`, `calendar_create` order. API/OAuth and tool wrappers stay in their existing packages, preserving `~/.gg/connectors/google.json`, permissions, refresh behavior, schemas, approval descriptions, coding send defaults, and personal draft defaults. No new background resource or cleanup hook is introduced; browser/MCP resources retain the host's existing cleanup. Core retains config/profile selection, authorization, and common lifecycle rules. OAuth scopes and tokens do not substitute for gg approval or standing grants.

First-party packages ship and migrate with a gg release. There is no separate installer, npm dependency resolver, dynamic Go plugin loader, or hot-reload lifecycle in this plan. The stable SPI is a maintained source-level contract, not a promised binary ABI or third-party in-process loading API.

## Profiles and coding compatibility

[`config.ParseProfile`](../internal/config/config.go) accepts `coding` and `personal`; unset or empty selects coding. C2 makes `--profile` / `Config.Profile` select the enabled tool/connector set as well as the [existing personal identity and defaults](personal-agent.md#selecting-a-profile-p3). No new CLI flag, saved config syntax, or daemon selector is added. Daemon, Web/Electron, Telegram, and daemon-scheduled turns retain their documented coding profile behavior; an embedding host that supplies a personal runtime config gets matching `system.info` capabilities.

Core resolves these built-in defaults in registration order:

| Order | Capability / provider | Coding (unset, empty, or explicit) | Personal | Existing prerequisite |
| --- | --- | --- | --- | --- |
| 1 | Core (unnamed) | Enabled | Enabled | None; retains `read`, `list`, `grep`, `bash`, `edit`, `write`, `subagent`. |
| 2 | `memory` | Enabled | Enabled | Memory enabled in config. |
| 3 | `kb` | Enabled | Enabled | Local knowledge index exists. |
| 4 | `artifact` | Enabled | Enabled | Artifact store opens successfully. |
| 5 | `connector` | Enabled | Enabled | Google token and granted scopes select tools; existing OAuth/refresh behavior. |
| 6 | `media` | Enabled | Disabled | Media endpoint configuration. |
| 7 | `browser` | Enabled | Enabled | Local Chromium is available. |
| 8 | `computer` | Enabled | Disabled | Linux or macOS backend. |
| 9 | `mcp` | Enabled | Disabled | Configured, reachable MCP servers. |

Coding retains all registered providers, including host extensions, with the pre-C2 order, schemas, defaults, and approval behavior. Personal keeps only unnamed core providers and the named capabilities above, in the host's registration order; other host-injected names are disabled. The smaller personal surface omits media clients, local-computer wrappers, and external MCP sessions. Core tools such as `bash` remain available: this selection is not a sandbox or an OS permission boundary. All currently registered connectors share the `connector` capability; C2 adds no per-connector selector.

Definitions and execution both use `Registry.Build`; capability reporting uses the same `ForProfile` selection and platform gates without building providers. Disabled providers are skipped before their `Available` probes or builders, open no provider resources, and are not reported as degraded. Enabled providers still apply existing runtime prerequisites and degradation rules. Capability identifiers describe enabled, platform-supported providers, not a guarantee of connected tokens, installed Chromium, or reachable servers; missing prerequisites can yield no tools for a turn. Unavailable connectors are skipped before opening their token store.

Personal's approval default remains `on-request`, yolo remains rejected, and omitted Gmail `as_draft` remains `true`; coding retains `auto` and default send behavior. Drafts and sends still require a human decision or a matching explicit standing grant for E/U. Profile selection neither creates standing grants nor bypasses approval. Composition is deterministic within the existing agent loop. C3 expands in-tree implementation splits; it does not land in C2.

## Package ownership inventory at `main@932e14e`

The following inventory covers all **29 top-level directories** under `internal/`, checked against the baseline tree and working checkout. Each appears once. These are responsibility classifications, not a claim that every file in a mixed package must move together:

- **Core** retains shared runtime, contracts, infrastructure, and features with no extraction planned here. Only the invariants above are permanent constraints; this label is not a promise that every implementation file stays forever.
- **First-party plugin (candidate)** identifies a later extraction candidate, still in-tree and statically linked. Shared contracts inside a mixed package remain Core-owned.
- **Adapter/surface** maps an external interface or protocol to Core. It does not gain policy ownership or automatically become a plugin.

| Top-level package | Classification | Ownership / later boundary |
| --- | --- | --- |
| `internal/agent` | Core | Agent/provider/tool contracts, loop, execution, approval requests, and consequence routing. |
| `internal/app` | Core | Session use cases, runs, prompt assembly, registry host, resource ownership, and shared authorization integration; candidate-specific builders may later delegate through SPI. |
| `internal/artifact` | First-party plugin (candidate) | Optional later extraction of versioned deliverables and publishing support if binary-size work is justified; preserve publish consistency and storage behavior. |
| `internal/bootstrap` | Core | Shared dependency construction for CLI and daemon. |
| `internal/browser` | First-party plugin (candidate) | Chromium/CDP capability implementation; preserve navigation approval and existing SSRF boundaries. |
| `internal/cli` | Adapter/surface | Command/flag parsing; delegates profile validation to Core config and preserves existing defaults. |
| `internal/cliapp` | Adapter/surface | CLI composition, management commands, approval presentation, and interaction with Core use cases. Shared approval semantics remain Core-owned. |
| `internal/config` | Core | Configuration resolution, validation, profile parsing, and defaults. |
| `internal/connector` | First-party plugin (candidate) | Connector implementations, with nested `google` preferred for C1. Separate the Core Connector contract/common lifecycle from provider operations; no blanket move of OAuth/token infrastructure. |
| `internal/contextmgr` | Core | Context budgeting, compaction, and conversation projection. |
| `internal/daemon` | Core | Composition root, channel supervision, scheduling/channel wiring, and process lifetime; later delegates candidate implementations. |
| `internal/filelock` | Core | Shared cross-process persistence locking. |
| `internal/kb` | Core | Knowledge-index build/search/storage support; no extraction planned in C0–C3. |
| `internal/library` | First-party plugin (candidate) | Optional later extraction of the curated file collection, paired with artifact/publish contracts if binary-size work is justified. |
| `internal/mcp` | Adapter/surface | External MCP connection, schema, and tool bridge. Core approval enforcement remains mandatory. |
| `internal/media` | First-party plugin (candidate) | Image/speech/transcription clients and capability implementation. |
| `internal/memory` | Core | Memory storage contracts, scopes, migration, permissions, and retrieval. |
| `internal/proactive` | First-party plugin (candidate) | Local delivery policy implementation; cannot grant authorization or replace memory-write gates. |
| `internal/provider` | Adapter/surface | Model-provider factory and nested protocol clients; `agent.Provider` and loop semantics remain Core contracts. |
| `internal/runlog` | Core | Shared structured logging and run context. |
| `internal/scheduler` | Core | Job definitions, durable store, firing loop, and unattended execution/approval contract. |
| `internal/session` | Core | Session repository, JSONL/tree contracts, leases, conflict checks, and recovery. |
| `internal/skills` | Core | Skill discovery and prompt integration; markdown skills use existing tools and do not create another plugin runtime. |
| `internal/standingauth` | Core | Grant persistence, matching, expiry/revocation, and allow audit. |
| `internal/telegram` | First-party plugin (candidate) | Channel API, bot/update handling, and voice integration; daemon lifecycle and shared approval remain in Core. |
| `internal/tools` | Core | Coding tools and shared approval/path/command policy; mixed browser/connector/media wrappers may later follow their capability through SPI. No wholesale tools move. |
| `internal/transport` | Core | JSON-RPC, stdio, HTTP/SSE contracts and enforcement. These are transport adapters in implementation, but remain part of the Core protocol boundary. |
| `internal/userprofile` | Core | `USER.md` parsing, rendering, and storage contract. |
| `internal/workspace` | Core | Workspace identity, roots, registry, and project instruction lookup. |

This is the C0 baseline inventory, not a directory-move checklist. In C1, the Google registration adapter uses the Connector SPI within `app`; `app` and `tools` still import candidate implementations directly. Other candidate dependencies have not been inverted.

## Phased delivery and acceptance

| Phase | Scope | Acceptance boundary |
| --- | --- | --- |
| **C0 — documentation (landed)** | This decision, Chinese mirror, and links from both architecture overviews. | Documentation only; all 29 packages classified; no runtime/SPI code or coding-default changes. Run `go vet ./...` and `go test ./...` and verify the diff contains only the four intended docs. |
| **C1 — Registry + Tool/Connector SPI (landed)** | `app.Registry`, Tool/Connector contracts, shared build/discovery assembly, and the statically linked Google registration pilot. No profile enable-list filtering. | Preserve tool names/schemas/order, discovery, defaults, prerequisite/degradation behavior, approval/standing-auth/E/M/U/R, storage paths, and resource cleanup. Existing tests and focused SPI/pilot checks pass. |
| **C2 — profile enable lists (landed)** | `Registry.ForProfile` filters construction and capability reporting; personal omits media/computer/MCP. | Definitions, execution, and advertised capabilities share selection; disabled providers are not probed or built. Unset/explicit coding retains its existing surface and defaults. Profile, platform, resource, and approval checks pass. |
| **C3 — expand in-tree splits (deferred)** | Extend the proven seam to browser/media and channel/policy implementations such as Telegram/proactive. Artifact/library remain optional later work. | Incremental migrations, green tests, stable Core ownership, and preserved channel supervision, approvals, and coding behavior. No big-bang `internal/` move. |
| **C4 — later external ecosystem** | Third-party remains MCP-first. Reassess distribution/market needs only against a concrete requirement. | No Bun host, npm market, hot reload, or new in-process third-party runtime is authorized here; any such proposal requires a new project decision. |

C1 establishes behavior-preserving boundaries before C2 changes selection. C3 expands only after that seam is proven. Moving code or adding an SPI does not itself justify new profile defaults, live triggers, privileges, or data migrations that change behavior.

## Non-goals and delivery rules

- No empty replacement repository or restart of the product from a skeleton.
- No rewrite of Core in Node, Bun plugin host, npm market, or hot reload.
- No big-bang relocation of `internal/`, runtime extraction in C0, or premature binary-size claim.
- No weakening of coding defaults, approval, standing-auth, or consequence semantics. Plugins cannot bypass these gates.
- No force-push or self-push. C2 is committed on `feat/core-plugin-c2`; the coordinator handles pushing and PR creation.

For C2, review evidence includes pre-C2 coding registration/tool order and definition parity, personal discovery/execution selection, host extension filtering, platform gates, skipped-provider resources, profile-aware `system.info`, preserved Gmail draft/approval behavior, and the existing standing-auth, connector refresh, and cleanup tests, plus `go vet ./...` and `go test ./...`. The PR body records what changed, why, tradeoffs, and the actual verification results. Package moves and broader in-tree splits remain C3; market/Bun work remains outside this delivery.
