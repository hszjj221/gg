# Personal Agent: proactive policy and schedulable local digests (P4c)

English | [简体中文](personal-agent.zh-CN.md)

P0 defined the consequence language and approval baseline. P1 added consequence routing, memory-write approval, and Gmail drafts. P2 added explicit standing grants and audited matching. P3 added a personal profile with its own base identity and stricter defaults. P4 adds a deterministic proactive policy and a runnable fake dogfood path that checks those brakes. P4c makes local stub Policy digests schedulable without an agent turn, model, or outbound delivery. The default `gg` CLI remains coding-first.

## Personal and coding agents

gg is primarily a terminal **coding** agent, as the [README](../README.md) describes. Both profiles use the [personal layer](architecture.md#personal-layer-profile--memory): `USER.md` supplies the user profile, and memory supplies durable context. The runtime profile selects the assistant's identity and defaults; it is separate from that user context.

P3 shares sessions, providers, tools, approvals, and standing authorization while separating **identity and default policy**. The personal face is the user's personal assistant, preferring private, reversible work and drafts. Project `AGENTS.md` still applies in a repository; `USER.md`, memory, project instructions, and skills keep their existing assembly order after the selected base instructions.

P1 extends the existing `ApprovalDescriber` → `ApprovalRequest` → `Approver` path. It preserves `Risk` / `RiskReason`, approval modes for ordinary calls, and the bash classifier. Routing uses consequence attributes, not a list of tool names.

## Selecting a profile (P3)

```sh
gg                              # coding, unchanged
gg --profile coding             # explicit coding profile
gg --profile personal           # personal assistant face
gg --profile personal -p 'Prepare a reply draft'
```

`--profile` accepts only `coding` and `personal`; unset or empty means `coding`. The top-level selector applies per invocation, including resumed sessions. P3 adds no saved config default or second binary; P4b adds the stored job override described below.

C2 also filters enabled capabilities: personal keeps core tools, memory, KB, artifacts, Google connectors, and browser, while omitting media, computer, and MCP providers. Coding retains the full existing set. See the [ordered profile enable list](core-plugin.md#profiles-and-coding-compatibility) for prerequisites and host-injection behavior. The draft and approval defaults below remain unchanged; enabling a capability grants no authorization.

| Default / policy | Coding | Personal |
| --- | --- | --- |
| Base identity | Coding agent working in the user's project | User's personal assistant |
| Approval mode | `auto` | `on-request` |
| `--yolo` / `--approval yolo` | Existing behavior | Rejected, with a standing-auth hint |
| Omitted Gmail `as_draft` | `false`: send | `true`: create a draft, not sent |

Personal's default `on-request` requires stdin and stderr to be terminals; without them the CLI exits before calling the model. In a terminal, ordinary gated calls prompt; existing read-only and agent-area exemptions remain. Explicit `--approval auto`, `on-request`, or `never` overrides the default. `never` retains its historical behavior of allowing ordinary calls without prompting; it does not authorize asserted E/M/U or dangerous/confirm calls. For unattended use with standing grants, select `--approval auto` explicitly: unmatched gated calls are denied. Both profiles still require a human or matching standing grant for asserted E/M/U; grants never waive dangerous/confirm risks.

Personal Gmail calls with omitted `as_draft` create real drafts. Explicit `as_draft=false` can still request sending, through the same E/U approval path. Previews and results reflect the effective action. Both draft and send remain E/U; neither is automatically authorized. A failed draft never falls back to sending. Standing-grant argument limits still match the original explicit arguments: a grant constrained to `as_draft=true` requires the call to include it, even in personal mode.

The selected `Config.Profile` reaches the shared prompt and tool registry, including the personal capability cuts and Gmail draft default. P4b adds a job profile selector, persisted as optional `profile` in the job definition:

```sh
gg job add --profile personal --cron '0 9 * * *' --name local-digest \
  'Summarize recent unread mail and today’s calendar in this session. Do not send, create drafts or events, notify, or add memory.'
gg job show local-digest
gg job run local-digest
```

`ggd` honors a stored `coding` or `personal` profile for each scheduled session. An omitted or empty job profile inherits the daemon's existing coding configuration. For manual `gg job run`, a stored profile **wins over the parent CLI `--profile`**; when unset, the job inherits the parent selector, so `gg --profile personal job run ...` still works for legacy jobs. `job show` and `job list` display unset profiles as `(inherit; daemon: coding)`. Manual and daemon jobs retain their existing unattended approval policy (`--allow-all`) and standing-grant matching.

P4b enables scheduled personal agent turns through the existing `gg job` scheduler. P4c also supports scheduled local stub Policy digests without an agent turn, as described below. Web/Electron and Telegram profile selectors, outbound Telegram delivery, live mailbox Policy digests, and automatic trigger discovery beyond scheduled jobs remain deferred. The original P4 demo remains an explicit CLI invocation.

## Proactive policy (P4 first slice)

[`internal/proactive.Policy.Decide`](../internal/proactive/policy.go) is a pure function: no model, I/O, clock reads, or inferred authorization. Inputs are the runtime profile, scenario (`email_summary`, `calendar_reminder`, `standing_auth_check`), supplied local time, an explicit remember-later request, and any proposed notification or tool approval request. The demo and P4c local digest runner call it directly; digest jobs use the existing scheduler without starting an agent turn. Ordinary agent turns do not gain automatic proactive triggers.

| Decision | Meaning | Action in this slice |
| --- | --- | --- |
| **Summarize** | A brief digest would help now. | Print a local digest to stdout when a writer is supplied; P4c also saves private local files. No outbound delivery. |
| **Quiet** | Do not interrupt. | No payload, notification, or outbound message; diagnostics may show the decision and reason. |
| **Note-only** | Capture for later without surfacing content now. | Write a private local candidate file; do not insert it into durable memory. Using `memory_add` later still goes through its existing approval gate. |

Defaults and precedence:

- Coding (including an unset profile) returns Quiet/disabled. Unknown profiles and scenarios also stay quiet.
- Personal email/calendar digests may Summarize locally. Quiet hours are disabled by default; `QuietHours="22:00-07:00"` uses the supplied time's location, includes the start, excludes the end, and supports overnight windows. Equal endpoints or malformed windows are rejected; invalid policy input stays Quiet.
- An explicit remember-later request selects Note-only, including during quiet hours. It never changes authorization for a proposed tool or notification.
- OS `notify` is off, with a fixed maximum of **zero notifications per run** and no opt-in in this slice. A proposed notification returns Quiet; OS notifications count as E. The `notify` tool now implements `ApprovalDescriber` with E=true and uses the shared approval gate when available. C2 still disables computer tools in personal; this policy does not enable them.
- A proposed tool action returns Quiet and must use the existing Approver/standing-auth path. In particular, asserted E/M/U always requires human confirmation or a matching standing grant. `gmail_send`, including a real Gmail draft, and `calendar_create` retain P1–P3 gates. Neither Summarize nor Note-only authorizes any tool, and unknown tool metadata is not permission.

## Schedulable local Policy digests (P4c — landed)

```sh
gg job add --proactive-digest --profile personal --cron '0 9 * * *' --name morning-digest
gg job show morning-digest
gg job run morning-digest
gg --profile personal proactive digest

# Replay a local quiet window, or keep private candidates for later:
gg --profile personal proactive digest --quiet-hours 22:00-07:00 --at 23:00
gg --profile personal proactive digest --remember-later
```

`--proactive-digest` persists `proactive_digest: true` and defaults an omitted job `--profile` to `personal`. Digest jobs require personal; explicit coding or empty profiles are rejected. The prompt is optional and unused for digest jobs; ordinary jobs still require one. Both `ggd` and manual `gg job run` call the shared local runner before session creation or provider setup, record decisions and paths in job history, and create no agent session. Scheduled runs fire only while `ggd` is alive. Quiet hours remain disabled for jobs in P4c; the quiet-hours and remember-later flags belong to the direct digest command.

The direct command uses the real local wall clock by default. Optional `--at HH:MM` replays today's local time. Its inbox and agenda text are explicitly **stubs**, with no live Gmail/Calendar reads. Summarize writes `email_summary.txt` and `calendar_reminder.txt` under a unique 0700 `~/.gg/proactive/digest-<timestamp>-*/` directory, with 0600 files, and prints their bodies when a writer is supplied. Note-only writes candidate files and prints only paths and decision metadata. Quiet prints decisions and reasons without writing digest bodies; coding prints Quiet/disabled and creates no digest directory. Files remain local for inspection, are never loaded as memory, and may be removed afterward.

Every personal run checks real Gmail draft/send approval metadata against Policy and `standingauth.Approver`, using a fresh empty grant store inside its digest directory and the existing unattended approver. Both omitted `as_draft` and explicit `as_draft:false` must be denied; an unexpected allow, non-Quiet policy decision, or approval error fails the run. No tool is executed. Successful runs report **“Brakes held: local only; no model; no outbound”**. This path constructs no model/provider or connector clients and sends no Telegram, email, or OS notification.

## Run the fake dogfood path

No model key, Google connection, live mailbox, or terminal is required:

```sh
gg --profile personal proactive demo
# From a checkout: go run ./cmd/gg --profile personal proactive demo

# Suppress both digest payloads during fake local quiet hours:
gg --profile personal proactive demo --quiet-hours 22:00-07:00 --at 23:00

# Capture private candidates instead, including during quiet hours:
gg --profile personal proactive demo --remember-later --quiet-hours 22:00-07:00 --at 23:00
```

The default fake time is 10:00 in the machine's local timezone (`--at HH:MM` changes it). Fake inbox snippets produce a short digest; a fake event 30 minutes away produces a local reminder when policy permits, without calling `calendar_create`. Note-only writes 0600 candidate files into a fresh 0700 `~/.gg/proactive/demo-*` directory, resolved from `Config.HomeDir`. Only candidate paths/decision metadata are printed, not their content. Demo directories are retained for inspection and may be removed afterward; they are never loaded as memory.

The standing-auth scene uses real `GmailSendTool.ApprovalRequest` metadata (personal's omitted `as_draft` means draft, explicit `false` means send), the shared `standingauth.Approver`, and the CLI approver with no human available. A fresh empty grant store inside the demo directory isolates this check from real grants. Both attempts must be denied, or the command fails. It never executes either tool action or creates a grant. Successful runs exit 0 with **“brakes held”** lines. `gg proactive demo` under coding prints Quiet/disabled and creates no demo state. The demo returns before session, workspace, or memory setup and never constructs a live connector/model client.

Policy and CLI tests cover local digest visibility, Note-only capture, quiet hours, notify off, and denied draft/send attempts even when the real gg home contains a matching grant. Existing standing-auth tests cover matching-grant authorization and audit. No live credentials are needed in CI.

### Optional live read-only smoke

With a configured model and an existing Google connection (`gg connect google`), run in a terminal:

```sh
gg --profile personal -p 'Use gmail_search/gmail_read to summarize recent unread mail and calendar_agenda for today. Keep the digest in this session. Do not send, create drafts or events, notify, or add memory.'
```

This uses the normal personal-profile agent path, not the fake demo or automatic policy triggers. It is optional and excluded from CI. Gmail/Calendar reads contact Google and their results enter the configured chat provider's context; they are not local-only processing. Sending, real Gmail drafts, and event creation still require human approval or a matching explicit grant. Keep credentials in the existing local provider configuration/environment.

## P4 scope and stop condition

| Plan item | Landed in this slice | Deferred |
| --- | --- | --- |
| Proactive trigger policy | Deterministic Summarize/Quiet/Note-only decisions, personal-only defaults, quiet hours, notification suppression, and consequence boundary. | Automatic trigger discovery beyond scheduled jobs. |
| Scheduled personal turns (P4b) | Stored job profile selector; daemon honors it and manual runs prefer it over the parent CLI profile. | Web/Electron/Telegram profile selectors and outbound Telegram delivery. |
| Schedulable local Policy digests (P4c) | `gg proactive digest` and `gg job add --proactive-digest`; local stub Summarize/Quiet/Note-only without an agent turn, model, or outbound delivery. | Live mailbox Policy digests and outbound delivery. |
| First-user dogfood | Fake email digest, calendar reminder, and standing-auth denial scene through the discoverable CLI, with tests and optional live read-only instructions. | Automatic proactive outbound delivery. |
| Stop-condition pointer | Keep coding-first defaults and the shared runtime. | Physical repository split. |

**Plan stop condition:** only consider a physical repository split if personal defaults **systematically hurt coding**, evidenced by approval noise, tool loops, or README persona drift. The P4 slice does not split the repository, rewrite the agent loop, add a second runtime, or broaden MCP/bash consequence classification. Existing `gg job`/`internal/scheduler` schedule P4c local digest jobs; no new scheduler is introduced, and the original demo stays separate.

## Four consequence dimensions

Classify the consequence of a concrete call, using its arguments, destination, account, and context. Tool names are examples and inventory keys, not the primary routing rule. The same tool can have different consequences on different calls.

| Dimension | Definition | Boundary examples |
| --- | --- | --- |
| **R — Reversible** | Can the whole effect be undone cheaply and locally, without external parties noticing? Restoring bytes is insufficient if disclosure, interruption, spending, or attribution has already occurred. | An isolated local draft with recoverable prior content can be reversible. Sending a message, incurring a charge, or killing a process with unsaved state generally is not. A delete or cancel operation does not by itself undo the original effect. |
| **E — Externally visible** | Does the call leave a trace outside the machine that others, including service operators, can observe? Visibility does not require a public audience. For personal boundaries, this also includes OS notifications that surface content beyond the agent's private working context, even without network traffic. | Sending, posting, opening a third-party URL, uploading audio, creating a calendar invite, and displaying an OS notification. A read-only network request can still leave a provider-visible trace. |
| **M — Spends money or changes permissions** | Does the call incur cost beyond normal chat, or change authority or access? These are two reasons to mark this dimension, even when no public content is created. | Billable image, speech, transcription, or embedding API calls; OAuth or scope changes; granting access; destructive privilege changes. A free endpoint avoids spending, not any other consequence. |
| **U — Acts in the user's name** | Does the call send, post, or create something attributable to the user, including in a private account? This concerns representation, beyond merely using credentials for a read. | Email sent from the user's account, an event created on the user's calendar, or a post published as the user. An authenticated search alone does not imply this dimension. |

The dimensions are independent: a local permission change can be M without E; creating a private calendar event is U and leaves an E trace at the service; a local draft can be reversible while a sent version is not. A reversible effect is not automatically authorized. Read-only does not mean local, free, or private.

These dimensions describe tool effects. Data returned by local reads can also reach the configured chat provider as conversation context; that shared runtime data flow still matters, but is distinct from a tool making its own external request. Existing sensitive-read gates, such as clipboard approval, remain relevant even without a direct E/M/U effect.

The plan's routing principle is to use these attributes rather than enumerate action names. Deterministic evidence and explicit authorization establish the boundary. The model only fills gaps in **reversible versus irreversible** judgment; it cannot invent authorization, waive E/M/U consequences, or broaden a grant. Missing evidence stays unresolved rather than becoming permission. P1 implements the asserted E/M/U boundary below; unclassified calls retain their existing risk-based policy.

## P1 runtime behavior

These rules remain the fallback when no standing grant matches.

[`agent.ApprovalRequest`](../internal/agent/types.go) carries four optional `*bool` fields alongside `Risk` / `RiskReason`:

| Dimension | Go field | JSON key |
| --- | --- | --- |
| R | `Reversible` | `reversible` |
| E | `ExternallyVisible` | `externallyVisible` |
| M | `SpendsMoneyOrChangesPermissions` | `spendsMoneyOrChangesPermissions` |
| U | `ActsInUserName` | `actsInUserName` |

`nil` (omitted JSON) means unknown; explicit `false` means the dimension was assessed and is absent. Unknown is not a claim of safety or authorization. `RequiresHumanConfirm()` reports whether E, M, or U is explicitly true. This check precedes read-only shortcuts: R alone never grants permission or overrides risk.

- Interactive `runApprover` continues to ask the human. The CLI prompts for asserted E/M/U in a terminal, including under `--yolo`; `--approval never` or no terminal denies them.
- A nil approver uses `DefaultApprover`, which denies asserted E/M/U. `UnattendedApprover` also denies them, including with `AllowOrdinary` / scheduled `--allow-all`. Pre-approved local-write exemptions cannot bypass asserted E/M/U.
- Requests with unknown or false E/M/U keep existing approval-mode and Risk rules, including `dangerous` / `confirm` denial without a human. No standing grant is inferred.

P1 populates `gmail_send` and `calendar_create` with E/U=true, M=false, R=false. With D1b, `browser_navigate` asserts E=true, M/U=false, R=false: navigation leaves a request trace, without billing or acting as the user's named identity. `memory_add` carries E/M/U=false and R=unknown: it is a durable local write, and removing an entry cannot undo later use.

D1a has landed for media and MCP. `image_generate`, `tts`, and `stt` assert R=false, E/M=true, U=false: remote disclosure and billed API usage cannot be undone by deleting local files, and API-key calls do not act as the user on a third-party identity. Unknown MCP server tools assert R=false, E/M/U=true and `Risk=confirm`; standing grants cannot waive that risk, so human confirmation is required. C2 still omits media and MCP providers from personal.

D1b has landed for computer describers, browser M/U, artifacts, and classified bash calls. `process_kill` asserts R=false, E/M/U=false and `Risk=dangerous`; `clipboard_write` asserts E/M/U=false and `Risk=confirm`. These risks require a human even under yolo or unattended allow-all and cannot be waived by standing grants. `open` asserts R=false, M/U=false, with E=true only for a resolved HTTP(S) target. `clipboard_read` asserts E/M/U=false and keeps its sensitive-read approval gate. Both artifact tools assert R=true, E/M/U=false for their local, removable or versioned content.

Bash reuses the existing classifier and optional risk verdict: `readonly` asserts E/M/U=false with R unknown; `dangerous` / `confirm` fail closed with R=false, E/M/U=true. Risk and RiskReason stay unchanged. Ordinary bash with empty risk leaves R/E/M/U unknown, so standing grants still cannot match it; its ordinary approval policy remains in effect.

`memory_add` implements `ApprovalDescriber` in both profiles. Its approval preview shows content, scope, and requested workspace/global target (all writes use global memory without a workspace context). `memory_search` remains ungated and read-only.

`notify` implements `ApprovalDescriber` on the shared runtime, with E=true because OS notifications surface content and may interrupt the user. Its preview shows truncated title/body; D1b adds M/U=false while leaving R unknown. C2 still disables computer tools in personal.

`gmail_send` accepts optional `as_draft` (default `false` in coding, `true` in personal). With `true`, it creates a real Gmail draft, returns the **draft ID**, and does not send; an API failure never falls back to sending. The preview and result say “not sent.” A draft still has E/U=true because Google sees it and it is created in the user's mailbox as them. Its content can be deleted, but the whole effect remains R=false because provider-visible traces cannot be undone locally. Both modes require human confirmation or a matching explicit standing grant.

Draft creation requires `gmail.compose`, now included in newly requested Google scopes alongside the existing scopes. Existing connections can still send; to use drafts, re-authorize with `gg connect google --force`. A missing draft scope produces an error with a reconnect hint. See [Connectors](connectors.md#security-boundaries).

## Standing authorization boundary (P2)

Standing authorization must be explicit, with all three parts:

| Part | Required meaning |
| --- | --- |
| **Subject** | Who may act, and on behalf of which user or account. |
| **Scope** | Which resources, destinations, and effects are allowed, with limits such as recipients, spending, or permitted permission changes. A broad goal alone is not a scope. |
| **TTL** | The grant's explicit validity window or expiry. Do not infer indefinite authorization or reuse an expired grant. |

User preferences in `USER.md`, remembered habits, an OAuth connection, or a previous approval do not by themselves establish a standing grant. P2 requires a **plain-text source quote** of the user's authorization as well as all three parts. “你看着办,” “随便,” “you figure it out,” “do what you think is best,” “whatever,” and “as you like” are rejected, including within longer quotes. Missing structured fields and a goal without tool/resource/effect limits are rejected before storing anything. Quotes are recorded, never interpreted by a model to derive permission.

### Storage and CLI

[`internal/standingauth`](../internal/standingauth/) stores one JSON file per grant at `~/.gg/standing-auth/<id>.json`, resolved from the same `Config.HomeDir` used by other gg state. The directory is 0700; grant files and `audit.jsonl` are 0600. Atomic file publication lets the CLI and running daemon share grants without restarting. Each record contains:

| Field | Meaning |
| --- | --- |
| `id`, `createdAt` | Generated stable ID and UTC creation time (validity starts here). |
| `subject` | Explicit string naming gg as actor and the user/account it represents, supplied by the user. |
| `scope.tools` | Nonempty allowlist of exact tool names, without wildcards. |
| `scope.consequences` | Explicit allowed E/M/U effects; `[]` allows none. R never grants permission. |
| `scope.arguments` | Nonempty JSON object of exact top-level argument limits, such as recipient and draft mode. Unspecified arguments remain unrestricted. |
| `expiresAt` | Required future RFC3339 expiry, computed from `--ttl` or provided via `--expires`. No indefinite default. |
| `sourceQuote`, `notes` | Required plain-text authorization quote and optional notes. |

```sh
gg standing-auth add \
  --subject 'gg on behalf of alice@example.com' \
  --tools gmail_send --consequences E,U \
  --arguments '{"to":"bob@example.com","as_draft":true}' \
  --ttl 24h \
  --source-quote 'For 24 hours, gg may create Gmail drafts as alice@example.com addressed only to bob@example.com; do not send.'
gg standing-auth list
gg standing-auth revoke <id>
```

Add prints the ID. Use `--expires '2026-12-01T09:00:00+08:00'` instead of `--ttl` for an absolute expiry; `--notes` is optional. Use `--consequences none` for explicitly local effects. List shows subject, structured scope, expiry/status, and source quote, including expired grants. Revoke is safe to repeat; it prevents subsequent matches, but cannot undo an already approved call.

The example allows any subject/body to that exact recipient **only when `as_draft` is explicitly `true`**. Missing `as_draft`, sending instead, a different recipient, or extra asserted M consequences cannot match. Add further exact argument limits to restrict content. P2 has no recipient patterns, spending counters, or interpretation of free-form scope text.

### Shared matching and audit

The shared turn executor wraps the supplied approver with `standingauth.Approver`, covering interactive CLI, `--approval never`, `--yolo`, nil/default approvers, manual/daemon jobs, and Telegram. Only approval-gated tools participate; existing local pre-approval exemptions remain. Embedders without a resolved `Config.HomeDir` retain P1 behavior rather than loading grants from the working directory.

Before asking or denying, the wrapper reads current grants in ID order. A match requires current validity, an exact tool, all E/M/U metadata known, every asserted effect included in the scope, and every constrained argument present and exactly equal as a JSON value. Unknown consequences miss; scopes from separate grants are never combined. `dangerous`, `confirm`, and unknown Risk classes **never match**, even with an E/M/U grant. P2 offers no flag to waive those classes; the existing human approval/denial path handles them.

On a hit, gg appends and syncs an audit record containing `timestamp`, `grantId`, `toolName`, `summary`, and `decision="allow-by-standing-grant"`, then allows without prompting. Storage corruption/read errors or an audit write failure fail closed. The audit records authorization, not successful execution. On a miss, the existing P1 approver runs unchanged.

Grants apply across profiles, workspaces, and transports using the same gg home. `subject` records the explicitly declared actor/account; selecting a profile does not scope a grant or verify an OAuth account. Keep the configured account consistent with that declaration and revoke/recreate grants when changing it. Telegram's chat allowlist still controls who may initiate turns; Telegram remains `UnattendedApprover{}`, never opts into AllowOrdinary.

Scheduled `--allow-all` is retained for backward compatibility: it still permits ordinary tools under P1 rules, but cannot waive asserted E/M/U or dangerous/confirm risks. Matching standing grants are the only unattended authorization path for asserted E/M/U in these approval-gated calls; no `--allow-all` is needed for a hit.

### TODO / follow-ups

- D8 landed: field/flag naming migrated to `AllowOrdinary`; JSON `auto_approve` and CLI `--allow-all` retained for compatibility; policy unchanged.
- D1a media/MCP and D1b computer describers, browser M/U, artifacts, and classified bash consequences have landed. Ordinary bash with empty risk remains unclassified for grants; follow up on that gap without forcing confirmation on every ordinary command. MCP, process termination, clipboard replacement, and dangerous/confirm bash remain ineligible for standing-grant matching because of their risk. Tools without `ApprovalDescriber` remain outside this gate; C2 personal still disables computer.
- Account/actor verification, transport-specific grant subjects, spending/usage limits, and richer destination limits remain follow-ups.

## Current approval inventory

P1 inventory, following P0 (#54), updated through D3, D1a, and D1b and checked against [built-in implementations](../internal/tools/), the [tool registry](../internal/app/toolregistry.go), and the [MCP bridge](../internal/mcp/tool.go). It covers **30 built-in tools: 16 with and 14 without `agent.ApprovalDescriber`**, plus every dynamically registered MCP tool. Registration depends on configuration, granted scopes, available providers, and platform support; not every session exposes all tools.

[`agent.ApprovalDescriber`](../internal/agent/types.go) is the interface with the `ApprovalRequest` method. “Yes” means the tool supplies an approval request to the [runner](../internal/agent/runner.go), not that every call produces a human prompt. Existing policy can allow or deny it, and `write`/`edit` have a narrow pre-approval exemption. “No” means this interface does not gate the tool; it does not mean the tool has no consequences.

The tables use R/E/M/U as shorthand for the dimensions above. They describe effects, not a claim that every tool already populates runtime metadata. R describes reversibility of the whole effect; “depends” requires call-specific evidence. E/M/U entries describe typical or conditional direct effects, not an exhaustive policy. Synced destinations, application behavior, and remote endpoints can change the assessment.

### Tools that implement ApprovalDescriber

| Tool | ApprovalDescriber? | Typical consequence dimensions | Notes / personal gap? |
| --- | --- | --- | --- |
| [`write`](../internal/tools/write.go) | Yes | R: depends on recovering overwritten content; usually local. | Creates/overwrites workspace files. Writes in the real `.gg/agent/` directory are pre-approved; this exemption stays unchanged. |
| [`edit`](../internal/tools/edit.go) | Yes | R: depends on restoring prior content; usually local. | Exact replacements with a before/after preview. Same real `.gg/agent/` pre-approval exemption. |
| [`bash`](../internal/tools/bash.go) | Yes | `readonly`: R unknown, E/M/U=false; `dangerous` / `confirm`: R=false, E/M/U=true; empty risk: all unknown. | D1b maps the existing risk verdict without a second classifier. Risk/reason and ordinary approval policy are preserved; empty-risk bash still cannot match grants. Dangerous/confirm risks require a human regardless of grant scope. |
| [`memory_add`](../internal/tools/memory_add.go) | Yes | R: unknown, depends on removing the entry before reuse; E/M/U: false. | Durable workspace/global write, now approval-gated on the shared runtime. Preview shows content, scope, and target; may influence later turns. |
| [`browser_navigate`](../internal/tools/browser.go) | Yes | R=false; E=true; M/U=false. | D1b completes M/U: navigation is neither billing nor acting as the user's named identity. Initial URL is SSRF-checked; approval covers navigation. Redirect/subresource limitations remain as documented in architecture. |
| [`gmail_send`](../internal/tools/connectors.go) | Yes | R: no for the external trace; E, U. | Coding defaults to send; personal defaults to draft. Explicit `as_draft` overrides either default. Both modes assert E/U. Deleting draft content does not undo provider traces. |
| [`calendar_create`](../internal/tools/connectors.go) | Yes | R: no for the external trace; E, U. | Creates on the primary calendar under the user's account. Current arguments do not include attendees; invite sending is a boundary example, not a claim about this tool. |
| [`artifact_create`](../internal/tools/artifact.go) | Yes | R=true; E/M/U=false. | D1b classifies local creation, which can be deleted. Writes the local artifact store outside the workspace. Does not publish to an external service. |
| [`artifact_edit`](../internal/tools/artifact.go) | Yes | R=true; E/M/U=false. | D1b classifies local versioning; prior content remains recoverable. Library publish is a separate application/CLI operation, not this tool. |
| [`image_generate`](../internal/tools/media.go) | Yes | R=false; E/M=true; U=false. | D1a populates all four dimensions for the billed API-key call. Sends the prompt to the media endpoint and saves images locally; deleting output does not undo disclosure or a charge. |
| [`tts`](../internal/tools/media.go) | Yes | R=false; E/M=true; U=false. | D1a populates all four dimensions for the billed API-key call. Sends text to the speech endpoint and saves audio locally. |
| [`stt`](../internal/tools/media.go) | Yes | R=false; E/M=true; U=false. | D1a populates all four dimensions for the billed API-key call. Uploads local audio for transcription; it is not merely a local file read. |
| [`process_kill`](../internal/tools/computer.go) | Yes | R=false; E/M/U=false. | D1b classifies the direct local SIGTERM/SIGKILL and adds `Risk=dangerous`, preventing yolo/allow-all or grant bypass. Restarting does not necessarily recover process state or undo interruption. |
| [`open`](../internal/tools/computer.go) | Yes | R=false; E=true for resolved HTTP(S) targets, false otherwise; M/U=false. | D1b classifies the resolved target handed to the default application. Relative files resolve against the workspace. |
| [`notify`](../internal/tools/computer.go) | Yes | R unknown; E=true; M/U=false. | D3 added approval with a title/body preview and E=true; D1b completes M/U. C2 still disables computer tools in personal. |
| [`clipboard_read`](../internal/tools/computer.go) | Yes | R unknown; E/M/U=false. | D1b classifies the direct local read. Deliberately gated because clipboard contents may be sensitive; later disclosure is not undone by clearing the clipboard. |
| [`clipboard_write`](../internal/tools/computer.go) | Yes | R unknown; E/M/U=false. | D1b classifies direct local replacement and adds `Risk=confirm`, preventing yolo/allow-all or grant bypass. Dependent applications may consume the new text. |
| [`mcp_<server>_<tool>`](../internal/mcp/tool.go) | Yes, all MCP tools | Actual effects depend on the server; D1a fails closed with R=false, E/M/U=true. | Every bridged MCP call carries `Risk=confirm`, including remote tools advertised as reads. Human confirmation is required; yolo, unattended allow-all, and standing grants cannot bypass it. C2 still omits MCP from personal. |

### Tools without ApprovalDescriber

| Tool | ApprovalDescriber? | Typical consequence dimensions | Notes / personal gap? |
| --- | --- | --- | --- |
| [`memory_search`](../internal/tools/memory_search.go) | No | R: local read; no direct E/M/U. | Read-only keyword search across workspace/global memory. Its lack of approval is also asserted in `memory_add_test.go`. |
| [`read`](../internal/tools/read.go) | No | R: local read; no direct E/M/U. | Reads workspace files or configured extra read roots. Sensitive content still enters the conversation. |
| [`list`](../internal/tools/list.go) | No | R: local read; no direct E/M/U. | Lists workspace directory entries. |
| [`grep`](../internal/tools/grep.go) | No | R: local read; no direct E/M/U. | Searches workspace text files. |
| [`browser_read`](../internal/tools/browser.go) | No | R: reads the current page; no new direct E/M/U. | Deliberate exemption: navigation is gated; subsequent reads of that page do not navigate again. |
| [`browser_screenshot`](../internal/tools/browser.go) | No | R: removable local capture; no new direct E/M/U. | Same deliberate navigation boundary; saves a PNG locally. It does write a file despite not navigating. |
| [`gmail_search`](../internal/tools/connectors.go) | No | R: no for provider request traces; E. | Read-only Gmail API search; third-party access is not local-only work. |
| [`gmail_read`](../internal/tools/connectors.go) | No | R: no for provider request traces; E. | Reads an email through the Gmail API; does not send as the user. |
| [`calendar_agenda`](../internal/tools/connectors.go) | No | R: no for provider request traces; E. | Reads upcoming events through the Calendar API; creates no event. |
| [`computer_info`](../internal/tools/computer.go) | No | R: local read; no direct E/M/U. | Reads OS, host, CPU, memory, and disk information. |
| [`process_list`](../internal/tools/computer.go) | No | R: local read; no direct E/M/U. | Inspects running processes; does not terminate them. |
| [`subagent`](../internal/tools/subagent.go) | No | R: depends on provider trace; E if the chat provider is remote. | Child tools are restricted to `read`/`list`/`grep`. Uses additional normal chat calls; ordinary chat cost alone is not M. Read-only child tools do not imply entirely local processing. |
| [`kb_search`](../internal/tools/kb_search.go) | No | R: depends; E if embeddings endpoint is remote, M if billed. | Read-only over the local index, but embeds the query through the configured API. Additional personal-mode review point for disclosure/cost; “search” does not guarantee no external consequence. |

## Existing policy and phase boundary

Current runtime policy is documented in the architecture's [Security model](architecture.md#security-model) and [Approval modes](architecture.md#approval-modes). P1 extends #53's fail-closed nil-approver handling for `dangerous`/`confirm` risks to asserted E/M/U consequences, while retaining existing Risk behavior for unclassified tools.

P1 shipped the fields, routing, memory-write gate, and optional Gmail draft switch described above. P2 shipped explicit standing authorization with durable storage, CLI management, shared matching, and an allow audit. P3 shipped the personal profile selector, personal base identity, stricter personal defaults, and Gmail draft preference while retaining the shared runtime. P4's first slice ships the local proactive policy and fake dogfood CLI above; P4b adds stored job profiles for scheduled personal turns; P4c adds schedulable local stub Policy digests without a model or agent turn. Live mailbox Policy digests and outbound delivery remain deferred under the stated stop condition.
