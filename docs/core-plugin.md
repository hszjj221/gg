# Core + first-party plugins

[中文](core-plugin.zh-CN.md)

This is the C0 architecture decision for the approved Core+plugin plan, based on `main@932e14e`. C0 changes documentation only: it introduces no registry or SPI implementation, moves no packages, and changes no tool set, profile default, approval policy, or runtime behavior. The [architecture overview](architecture.md) describes the running system; the phases below describe future work.

## Goal and boundaries

Keep one Go Core with a stable service provider interface (SPI). First-party plugins remain in-tree Go packages, compiled into the same binary and assembled by Core. A profile selects an ordered enable list and defaults within that runtime. Third-party capabilities remain MCP-first, using the existing external-process or HTTP boundary.

The purpose is to make capability ownership and assembly explicit without weakening the coding product. A plugin boundary does not require a separate process, a separately installed package, or a smaller binary. Static linking gives first-party capabilities the same build, review, and release lifecycle as Core; profile filtering later reduces the enabled capability surface, not the linked binary size.

First-party candidates for later phases are:

- **Connectors**, with `internal/connector/google` preferred as the C1 pilot.
- **Browser** and **media** capability implementations.
- **Telegram channel** and **proactive delivery policy** implementations.
- **Artifact/library**, optionally later if reducing binary size becomes a concrete requirement. An enable list alone cannot remove their linked code.

C0 lists candidates; it does not authorize a bulk directory move or define the final SPI types.

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

## SPI and package lifecycle direction

The current seam is [`app.ToolProvider`](../internal/app/toolregistry.go): a named tool builder, an optional platform availability check, deterministic registration order, and host/test injection through `Options.ToolProviders`. [`conversationTools`](../internal/app/toolset.go) owns per-conversation resources and cleanup; definitions and execution already share this construction path. This is an implementation seam, not yet the stable plugin SPI.

C1 should derive a Registry and Tool/Connector SPI from that behavior. Exact package paths, exported Go types, and any version-negotiation mechanism belong to the C1 design and implementation review. The intended ownership contract is:

| Lifecycle area | Core / host | First-party implementation |
| --- | --- | --- |
| Registration and compatibility | Own identifiers, deterministic assembly, validation, and the SPI compatibility contract. | Register capability metadata and implementations through the agreed SPI. |
| Configuration and selection | Parse profiles/config and resolve enabled capabilities and defaults. | Consume validated capability configuration; preserve existing connection/scopes behavior. |
| Build and discovery | Use one selected registry for tool definitions, execution, and advertised capabilities. Preserve platform gates and expected-disabled versus degraded reporting. | Build tools/clients and report availability or operational failure through that contract. |
| Execution and trust | Keep agent dispatch, approvals, standing-auth, consequence interpretation, and audit. | Describe operation effects and execute only through Core's dispatch and approval path. |
| Resource lifetime | Own session/daemon lifetimes and call cleanup on shutdown or retirement. | Release owned clients, connections, and other resources through the host lifecycle. |
| Persistence and migration | Retain shared storage contracts and coordinate compatible startup/migrations. | Own capability-specific data and migration logic within that lifecycle; preserve existing paths and permissions during migration. |

For the preferred Google pilot, connector API operations and their tool wrappers are the candidate implementation. Core retains the Connector SPI, config/profile selection, authorization, and common lifecycle rules. OAuth scopes and tokens do not substitute for gg approval or standing grants. C1 must preserve tool names, schemas, order, defaults, token paths, refresh behavior, and error/cleanup semantics; extracting an interface alone is not permission to change behavior.

First-party packages ship and migrate with a gg release. There is no separate installer, npm dependency resolver, dynamic Go plugin loader, or hot-reload lifecycle in this plan. The stable SPI is a maintained source-level contract, not a promised binary ABI or third-party in-process loading API.

## Profiles and coding compatibility

At `932e14e`, [`config.ParseProfile`](../internal/config/config.go) accepts `coding` and `personal`; unset or empty selects coding. The [existing personal profile](personal-agent.md#selecting-a-profile-p3) changes CLI identity and defaults, including approval mode and Gmail draft preference. It does **not** yet implement profile enable-list filtering of the tool/connector registry. Daemon, Web/Electron, Telegram, and daemon-scheduled turns retain their documented coding profile behavior.

C2 will make `--profile` select the enabled tool/connector set as well as defaults. Core will resolve an ordered capability list, apply prerequisites/platform checks, and use the resulting set consistently for definitions, execution, and capability reporting. Disabled providers should not be built or open their resources. Composition order must be deterministic; a profile cannot install code, create authorization, override a Core invariant, or introduce its own agent loop.

The unset/empty and explicit `coding` paths must preserve the existing coding tools, order, defaults, and approval behavior. Personal filtering and any additional selector surfaces require explicit C2 scope and checks; C0 promises no new CLI flags, config syntax, or daemon selector. Personal's existing stricter defaults remain in place. Profile enablement never implies authorization to use an enabled tool.

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

The baseline is deliberately preserved: `app` and `tools` currently import several candidate implementations directly. The inventory assigns future ownership; it does not claim those dependencies have already been inverted.

## Phased delivery and acceptance

| Phase | Scope | Acceptance boundary |
| --- | --- | --- |
| **C0 — documentation** | This decision, Chinese mirror, and links from both architecture overviews. | Documentation only; all 29 packages classified; no runtime/SPI code or coding-default changes. Run `go vet ./...` and `go test ./...` and verify the diff contains only the four intended docs. |
| **C1 — Registry + Tool/Connector SPI** | Introduce the shared contracts and migrate existing registration with zero behavior change. Include one first-party example, preferably Google, still statically linked. | Preserve tool names/schemas/order, discovery, defaults, prerequisite/degradation behavior, approval/standing-auth/E/M/U/R, storage paths, and resource cleanup. Existing tests and focused SPI/pilot checks pass. |
| **C2 — profile enable lists** | Make `--profile` actually trim enabled tools/connectors through Core composition. | Definitions, execution, and advertised capabilities agree; disabled providers are not built. Unset/explicit coding retains its existing surface and defaults. Verify profile filtering and unchanged authorization. |
| **C3 — expand in-tree splits** | Extend the proven seam to browser/media and channel/policy implementations such as Telegram/proactive. Artifact/library remain optional later work. | Incremental migrations, green tests, stable Core ownership, and preserved channel supervision, approvals, and coding behavior. No big-bang `internal/` move. |
| **C4 — later external ecosystem** | Third-party remains MCP-first. Reassess distribution/market needs only against a concrete requirement. | No Bun host, npm market, hot reload, or new in-process third-party runtime is authorized here; any such proposal requires a new project decision. |

C1 establishes behavior-preserving boundaries before C2 changes selection. C3 expands only after that seam is proven. Moving code or adding an SPI does not itself justify new profile defaults, live triggers, privileges, or data migrations that change behavior.

## Non-goals and delivery rules

- No empty replacement repository or restart of the product from a skeleton.
- No rewrite of Core in Node, Bun plugin host, npm market, or hot reload.
- No big-bang relocation of `internal/`, runtime extraction in C0, or premature binary-size claim.
- No weakening of coding defaults, approval, standing-auth, or consequence semantics. Plugins cannot bypass these gates.
- No force-push. C0 is committed on `feat/core-plugin-c0`; the coordinator handles pushing and PR creation.

For C0, the review evidence is the four-file documentation diff plus `go vet ./...` and `go test ./...`. Those Go checks catch accidental build/test drift; they do not validate an unimplemented SPI. The PR body records what changed, why, tradeoffs, and the actual verification results.
