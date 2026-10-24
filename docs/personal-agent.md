# Personal Agent: boundaries and baseline (P0)

English | [简体中文](personal-agent.zh-CN.md)

P0 (definition and baseline) of the gg → Personal Agent plan defines consequence language and records current approval coverage. This phase changes documentation only; the policy described as a target below is future work.

## Personal and coding agents

Today gg is primarily a terminal **coding** agent, as the [README](../README.md) describes. The [personal layer](architecture.md#personal-layer-profile--memory) is already wired into the shared runtime: `USER.md` supplies the user profile, and memory supplies durable context. Having that context does not establish a separate personal identity or stricter default policy today.

The target keeps the shared runtime for sessions, providers, tools, and approvals. P3 will separate **identity and default policy**: coding defaults stay coding-friendly, while personal defaults become stricter about consequences outside private, reversible work. This is a policy and identity split, not a second runtime.

P0 succeeds when:

- P1 can add consequence fields beside `risk` on `agent.ApprovalRequest` without reopening the definitions below.
- The approval inventory matches the `origin/main` baseline, including deliberate exemptions and personal-mode gaps.
- This change leaves runtime behavior untouched: approval modes, CLI flags, `DefaultApprover`, fail-closed runner logic, tool defaults, `ApprovalDescriber` implementations, and tests asserting current approval behavior all stay unchanged.

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

The plan's routing principle is to use these attributes rather than enumerate action names. Deterministic evidence and explicit authorization establish the boundary. The model only fills gaps in **reversible versus irreversible** judgment; it cannot invent authorization, waive E/M/U consequences, or broaden a grant. Missing evidence stays unresolved rather than becoming permission. P0 establishes this vocabulary; it adds no consequence fields, routing logic, or new approval defaults.

## Standing authorization boundary (P2)

Standing authorization must be explicit, with all three parts:

| Part | Required meaning |
| --- | --- |
| **Subject** | Who may act, and on behalf of which user or account. |
| **Scope** | Which resources, destinations, and effects are allowed, with limits such as recipients, spending, or permitted permission changes. A broad goal alone is not a scope. |
| **TTL** | The grant's explicit validity window or expiry. Do not infer indefinite authorization or reuse an expired grant. |

User preferences in `USER.md`, remembered habits, an OAuth connection, or a previous approval do not by themselves establish a standing grant. P2 will implement standing authorization; P0 neither creates grants nor changes how existing approvals work.

## Current approval inventory

Baseline: `origin/main` at `8776b83` (#53), also the base of `docs/personal-agent-p0`. The inventory was checked against [built-in implementations](../internal/tools/), the [tool registry](../internal/app/toolregistry.go), and the [MCP bridge](../internal/mcp/tool.go). It covers **30 built-in tools: 15 with and 15 without `agent.ApprovalDescriber`**, plus every dynamically registered MCP tool. Registration depends on configuration, granted scopes, available providers, and platform support; this is not a claim that every session exposes all tools.

[`agent.ApprovalDescriber`](../internal/agent/types.go) is the interface with the `ApprovalRequest` method. “Yes” means the tool supplies an approval request to the [runner](../internal/agent/runner.go), not that every call produces a human prompt. Existing policy can allow or deny it, and `write`/`edit` have a narrow pre-approval exemption. “No” means this interface does not gate the tool; it does not mean the tool has no consequences.

The tables use R/E/M/U as documentation shorthand, not proposed API field names. R describes reversibility of the whole effect; “depends” requires call-specific evidence. E/M/U entries describe typical or conditional direct effects, not an exhaustive policy. Synced destinations, application behavior, and remote endpoints can change the assessment.

### Tools that implement ApprovalDescriber

| Tool | ApprovalDescriber? | Typical consequence dimensions | Notes / personal gap? |
| --- | --- | --- | --- |
| [`write`](../internal/tools/write.go) | Yes | R: depends on recovering overwritten content; usually local. | Creates/overwrites workspace files. Writes in the real `.gg/agent/` directory are pre-approved; this exemption stays unchanged. |
| [`edit`](../internal/tools/edit.go) | Yes | R: depends on restoring prior content; usually local. | Exact replacements with a before/after preview. Same real `.gg/agent/` pre-approval exemption. |
| [`bash`](../internal/tools/bash.go) | Yes | R/E/M/U: depend on command and targets. | Current classifier and optional risk check populate `risk`/`riskReason`. A shell command can have any of the four dimensions. |
| [`browser_navigate`](../internal/tools/browser.go) | Yes | R: no for a third-party request trace; E. | Initial URL is SSRF-checked; approval covers navigation. Redirect/subresource limitations remain as documented in architecture. |
| [`gmail_send`](../internal/tools/connectors.go) | Yes | R: no once sent; E, U. | Sends from the user's Gmail account. Removing a local copy cannot recall delivery or attribution. |
| [`calendar_create`](../internal/tools/connectors.go) | Yes | R: no for the external trace; E, U. | Creates on the primary calendar under the user's account. Current arguments do not include attendees; invite sending is a boundary example, not a claim about this tool. |
| [`artifact_create`](../internal/tools/artifact.go) | Yes | R: local creation can be removed; no direct E/M/U. | Writes the local artifact store outside the workspace. Does not publish to an external service. |
| [`artifact_edit`](../internal/tools/artifact.go) | Yes | R: prior content recoverable from retained versions; no direct E/M/U. | Appends a complete local version. Library publish is a separate application/CLI operation, not this tool. |
| [`image_generate`](../internal/tools/media.go) | Yes | R: no for remote disclosure or incurred cost; E if remote, M if billed. | Sends the prompt to the media endpoint and saves images locally. Deleting output does not undo a charge. |
| [`tts`](../internal/tools/media.go) | Yes | R: no for remote disclosure or incurred cost; E if remote, M if billed. | Sends text to the speech endpoint and saves audio locally. |
| [`stt`](../internal/tools/media.go) | Yes | R: no for remote disclosure or incurred cost; E if remote, M if billed. | Uploads local audio for transcription; it is not merely a local file read. |
| [`process_kill`](../internal/tools/computer.go) | Yes | R: generally no for lost process state; E possible for a connected service. | Sends SIGTERM or SIGKILL. Restarting a process does not necessarily recover its state or undo interruption. |
| [`open`](../internal/tools/computer.go) | Yes | R: depends; E for third-party URLs; other dimensions depend on the target/application. | Hands a file or URL to the default application. Assess the resulting effects, not just the local launch. |
| [`clipboard_read`](../internal/tools/computer.go) | Yes | R: local read; no direct E/M/U. | Deliberately gated because clipboard contents may be sensitive. Later disclosure is not undone by clearing the clipboard. |
| [`clipboard_write`](../internal/tools/computer.go) | Yes | R: depends on recovering the replaced clipboard; E possible with sync or external consumption. | Replaces system clipboard text. A local write can affect other applications or devices. |
| [`mcp_<server>_<tool>`](../internal/mcp/tool.go) | Yes, all MCP tools | R/E/M/U: depend on the server, operation, and arguments. | Every bridged MCP call is approval-gated, including remote tools advertised as reads. Coverage is universal; consequence classification is not yet supplied by this bridge. |

### Tools without ApprovalDescriber

| Tool | ApprovalDescriber? | Typical consequence dimensions | Notes / personal gap? |
| --- | --- | --- | --- |
| [`memory_add`](../internal/tools/memory_add.go) | No | R: depends on removing the entry before reuse; durable local mutation. | **Explicit personal-mode gap.** Appends workspace/global memory that can influence later turns. [`TestMemoryAddToolDoesNotRequireApproval`](../internal/tools/memory_add_test.go) asserts the absence of this interface. P1 will gate it at least in personal mode; P0 preserves implementation and test expectations. |
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
| [`notify`](../internal/tools/computer.go) | No | R: no once seen/interrupted; E (OS notification). | **Potential personal-mode gap.** Currently treated as harmless and ungated; content can be visible or interruptive on the OS surface. P0 records the gap without adding a gate. |
| [`subagent`](../internal/tools/subagent.go) | No | R: depends on provider trace; E if the chat provider is remote. | Child tools are restricted to `read`/`list`/`grep`. Uses additional normal chat calls; ordinary chat cost alone is not M. Read-only child tools do not imply entirely local processing. |
| [`kb_search`](../internal/tools/kb_search.go) | No | R: depends; E if embeddings endpoint is remote, M if billed. | Read-only over the local index, but embeds the query through the configured API. Additional personal-mode review point for disclosure/cost; “search” does not guarantee no external consequence. |

## Existing policy and phase boundary

Current runtime policy is documented in the architecture's [Security model](architecture.md#security-model) and [Approval modes](architecture.md#approval-modes). This includes #53's fail-closed nil-approver handling for `dangerous`/`confirm` risks via `agent.DefaultApprover`; P0 leaves it unchanged.

P1 will add consequence metadata beside the existing `risk` language and address coverage, including personal-mode memory writes. P2 will add explicit standing authorization. P3 will separate identity and default policy while retaining the shared runtime. None of those mechanisms, stricter defaults, or new gates is implemented by this document.
