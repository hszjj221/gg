# Core + 第一方插件

[English](core-plugin.md)

本文记录已批准的 Core+plugin 方案：C0 架构决策（基于 `main@932e14e`）、C1 Registry 与 Tool/Connector SPI（基于 `main@e77769a`）、C2 profile 启用列表（基于 `main@53fb4be`），以及 C3 仓库内注册拆分（基于 `main@3b2ab86`）。C0 仅修改文档；C1 建立仓库内 SPI，以静态链接的 Google 为试点。C2 按 profile 过滤 provider 构建与能力报告。C3 增加 browser/media 注册适配器，明确 Telegram/proactive 工厂边界，保留 coding 工具、默认值和审批策略。[架构总览](architecture.zh-CN.md)描述当前系统；通用 channel/policy SPI 与 C4 仍待后续实现。

## 目标与边界

保留一个 Go Core，并提供稳定的服务提供者接口（SPI）。第一方插件继续作为仓库内的 Go 包编译进同一个二进制，由 Core 组装。Profile 在这个运行时内选择有序的启用列表和默认值。第三方能力继续优先通过 MCP 接入，沿用外部进程或 HTTP 边界。

目标是明确能力归属与组装方式，同时保持 coding 产品体验。插件边界不要求独立进程、独立安装包或更小的二进制。静态链接让第一方能力与 Core 共用构建、审查和发布生命周期；C2 profile 过滤缩小的是启用的能力面，不是链接后的二进制体积。

第一方实现进度与后续候选包括：

- **Connectors**：Google 已作为 C1 试点通过 SPI 注册；OAuth/API 代码仍在 `internal/connector/google`。
- **Browser** 与 **media**：C3 通过 `app` 适配器注册专属 ToolProvider；实现和工具包装仍留在原包。
- **Telegram channel** 与 **proactive 投递策略**：C3 复用 daemon 的 Telegram 工厂，并增加 app 侧 proactive 策略工厂。通用 channel/policy SPI 延后。
- **Artifact/library**：仅在缩小二进制成为具体需求后，作为更后期的可选拆分。单靠启用列表无法移除已链接的代码。

C0 列出候选；C1 将 SPI 保留在 `app`，避免搬迁共享宿主依赖。C3 扩展注册接口，不搬迁实现包。任何阶段都不授权批量移动目录。

## Core 不变量

以下职责永远留在 Core，即使发起请求的实现属于插件：

| 职责 | Core 归属 |
| --- | --- |
| Agent loop 与 session | 轮次执行、取消、steering、会话身份、持久化、分支、并发、运行结果和事件回放。 |
| JSON-RPC 与 SSE | 方法与 DTO 契约、校验、能力报告、审批事件、重连/回放语义和传输访问校验。Channel 或插件使用这些契约，不能替换它们。 |
| 审批流水线 | Core 拥有执行门禁、风险路由、人工决策、无人值守拒绝和审计。插件操作必须经过这条路径，不能绕行执行。 |
| Standing authorization | 授权存储、显式来源、匹配、过期、撤销和放行审计。启用插件或 profile 永远不会创建 grant。 |
| 后果属性 | E/M/U 与 `reversible` 的共享含义及路由，包括未知值。实现描述后果，Core 应用策略。 |
| 配置与组装 | 配置解析与校验、daemon 组装与监督、Registry 组装、profile 解析、启用列表求值，以及 coding 默认值。 |
| Memory 与 user profile | Memory 和 `USER.md` 的存储契约、scope/路径规则、权限、迁移和共享提示词接入。插件使用这些契约，不能另建相互竞争的存储。 |

现有 [`agent.ApprovalRequest`](../internal/agent/types.go) 后果契约继续作为准则：

| 属性 | 含义 |
| --- | --- |
| E / `externallyVisible` | 本地进程之外可见的影响或痕迹，包括服务运营方可见的痕迹。 |
| M / `spendsMoneyOrChangesPermissions` | 普通聊天之外的花费，或权限/访问能力变化。 |
| U / `actsInUserName` | 创建或发送可归因于用户的内容，包括私人账户内的操作。 |
| R / `reversible` | 整个效果能低成本地在本地撤销，且不被外部主体察觉。R 独立于 E/M/U。 |

省略的后果属性表示未知，不能作为安全或已获授权的证据。已断言的 E/M/U 仍需人工决策或匹配的显式 standing grant，包括 coding `--yolo` 和定时任务 `--allow-all`。Standing grant 不能豁免 `dangerous`/`confirm` 风险；R 本身也不授权操作。C0 不扩大分类覆盖面，也不移除未分类调用现有的基于风险的兼容路径；详见[个人助手后果与审批清单](personal-agent.zh-CN.md)。

第一方插件以 gg 的 OS 权限执行。静态链接和 SPI 提供归属边界，不提供沙箱。实现必须保留现有审批描述，不能绕过 approval 或 standing-auth。Proactive 决定何时展示摘要属于投递策略，不代表获准发送、通知或写入记忆。D1a 保留 MCP 全量审批门禁，并对未知 server 工具以 R=false、E/M/U=true 和 `Risk=confirm` 失败关闭；必须人工确认。

## Magpie 与 dsh 的启示

下表记录已批准方案使用的比较，用来指导 gg 的归属决策，不引入其他项目的运行时或分发模式。

| 维度 | Magpie | dsh | gg 选择 |
| --- | --- | --- | --- |
| 宿主语言 | Go + Bun 插件 | Node/Cordis | 保留 Go Core。 |
| 插件形态 | 外部 npm/JS | npm + patch 层 | 优先仓库内 Go；外部能力通过 MCP。 |
| 组装 | Install/migrate 归属 | Profile bundle 栈 | Profile 启用列表 + 默认值。 |
| 信任 | 宿主强制协议 | 版本门禁 | Core 强制 approval/standing-auth/E·M·U，并保留 reversible 元数据。 |
| 市场 | 社区插件 | 插件管理 UI | 仅列为 C4+ 考量；当前不做市场。 |

**Magpie 启示：**明确 Core 与插件的归属，以及包的生命周期。能力必须有人负责注册、初始化、配置、资源清理、兼容性和数据迁移。gg 可以采用这种管理方式，同时把第一方代码继续编译进现有 Go 二进制。

**dsh 启示：**profile 是能力层的有序组合，以启用列表和默认值表达，不是第二套运行时。Core 仍负责组合求值、运行 agent 和强制授权。

## SPI 与包生命周期

C1 在 [`app/plugin.go`](../internal/app/plugin.go) 定义源码级 SPI，将 `ToolProvider` 和 `ToolContext` 保留在 `app`，采用更小的边界。`NewRegistry` 按注册顺序保存 provider 快照；C2 增加 `Registry.ForProfile`，在构建或能力报告前确定启用的有序子集。`Registry.Build` 根据 `ToolContext.Config.Profile` 选择；`ToolCapabilities(profile)` 返回选定 Registry 的 `Capabilities`。两条路径保留 `Available` 平台门禁。空名称仍表示不对外展示的 core provider。`Options.ToolProviders` 继续支持宿主/测试注入：nil 使用内置注册，非 nil 空切片禁用全部工具，fork 会话也保留此选择。Profile 过滤同样作用于宿主注入的具名 provider。

`ConnectorProvider` 以既有 token 标识命名 connector，接收宿主上下文、现有 store 与当前 token 来构建工具。`ConnectorTools` 将这些 provider 注册到既有 `connector` 能力下。缺失 token 仍是预期的未连接状态；store/token 故障继续交给现有降级报告。Tool provider 失败时丢弃该 provider 本轮工具，构建成功则清除旧故障。[`conversationTools`](../internal/app/toolset.go) 继续负责日志、脱敏降级报告和会话资源清理；发现与执行共用同一个 Registry。注册仍是受信任的 Go 代码组装，不新增元数据校验或版本协商协议。归属契约如下：

| 生命周期领域 | Core / 宿主 | 第一方实现 |
| --- | --- | --- |
| 注册与兼容性 | 拥有标识符、确定的组装、校验和 SPI 兼容契约。 | 通过约定 SPI 注册能力元数据与实现。 |
| 配置与选择 | 解析 profile/config，确定启用能力和默认值。 | 使用经过校验的能力配置，保留现有连接/scopes 行为。 |
| 构建与发现 | 工具定义、执行和对外能力共用选定的 Registry。保留平台门禁，以及预期禁用与运行故障的区分。 | 构建工具/客户端，通过契约报告可用性或运行故障。 |
| 执行与信任 | 保留 agent 分发、审批、standing-auth、后果解释与审计。 | 描述操作后果，仅通过 Core 的分发和审批路径执行。 |
| 资源生命周期 | 拥有 session/daemon 生命周期，在关闭或淘汰时调用清理。 | 通过宿主生命周期释放所拥有的客户端、连接和其他资源。 |
| 持久化与迁移 | 保留共享存储契约，协调兼容的启动/迁移。 | 在该生命周期内负责能力专属数据及迁移逻辑；迁移时保持既有路径和权限。 |

[`Google 试点注册`](../internal/app/googleplugin.go) 已使用 Connector SPI，保留按 scope 选择工具，以及 `gmail_search`、`gmail_read`、`gmail_send`、`calendar_agenda`、`calendar_create` 的既有顺序。API/OAuth 和工具包装继续位于原包，保留 `~/.gg/connectors/google.json`、权限、刷新行为、schema、审批描述、coding 默认发送和 personal 默认草稿行为。不引入后台资源或新的清理钩子；browser/MCP 资源沿用宿主清理。Core 保留 config/profile 选择、授权和公共生命周期规则。OAuth scopes 和 token 不能替代 gg 审批或 standing grant。

第一方包随 gg release 发布与迁移。本方案不引入独立安装器、npm 依赖解析器、动态 Go 插件加载器或热重载生命周期。稳定 SPI 是持续维护的源码级契约，不承诺二进制 ABI，也不提供第三方进程内加载 API。

### C3 扩大仓库内实现拆分

[`browserTools`](../internal/app/browserplugin.go) 与 [`mediaTools`](../internal/app/mediaplugin.go) 返回既有 ToolProvider 注册，Registry 中仍先 `media` 后 `browser`。Builder 保留工具顺序、schema、审批描述、STT 读取根目录和前置条件行为。两者的 `Available` 仍为 nil：能力报告不探测运行时前置条件；缺少 Chromium 或 media endpoint 时不构建工具，也不记录运行故障。Core 的 `ToolMemo` 仍拥有会话内 Chromium 探测与复用的 media client；browser pool 继续由现有宿主生命周期关闭。实现仍在 `internal/browser`、`internal/media` 和 `internal/tools`。

[`newTelegramChannel`](../internal/daemon/telegram.go) 已提供最小 channel 工厂，C3 保留并明确该接口，不增加重复包装。它将宿主配置、共享 runtime、logger、allowlist 和可选语音客户端传给 `telegram.New`，再将 bot 适配为 daemon `Channel`。Core 仍先构建所有已配置 channel 再启动任何一个；构建错误使启动失败，运行时错误/panic 由统一监督处理，取消和 worker 等待仍归 Core。Telegram 的 bot、update/语音处理、存储路径和审批路径不变。

[`app.NewProactivePolicy`](../internal/app/proactiveplugin.go) 为 CLI demo 构建并校验既有具体类型 `proactive.Policy`。策略决策仍在 `internal/proactive`；CLI flag 解析、本地输出/候选存储和既有 approver 仍由宿主负责。构建不增加 I/O 或后台工作。Coding 保持静默；personal 的 quiet hours、候选笔记、零通知，以及无人工或 grant 时拒绝草稿/发送的行为不变。

本次仅落地 ToolProvider 适配器和具体 channel/policy 工厂接口。通用 Channel/Policy SPI、更广泛的依赖倒置、包搬迁及 artifact/library 提取延后；本 PR 无需这些工作即可明确归属。不新增 profile 默认值、真实触发器、安装器或第三方 channel 市场。C4 仍为后续工作。

## Profile 与 coding 兼容性

[`config.ParseProfile`](../internal/config/config.go) 接受 `coding` 与 `personal`，未设置或空值选择 coding。C2 让 `--profile` / `Config.Profile` 同时选择启用的工具/connector 集合，以及[既有 personal 身份与默认值](personal-agent.zh-CN.md#选择-profilep3)。不新增 CLI flag、持久化配置语法或 daemon selector。Daemon、Web/Electron、Telegram 和 daemon 定时轮次继续保持已记录的 coding profile 行为；嵌入宿主若提供 personal runtime config，`system.info` 会报告相应能力。

Core 按注册顺序解析以下内置默认值：

| 顺序 | 能力 / provider | Coding（未设置、空值或显式） | Personal | 既有前置条件 |
| --- | --- | --- | --- | --- |
| 1 | Core（空名称） | 启用 | 启用 | 无；保留 `read`、`list`、`grep`、`bash`、`edit`、`write`、`subagent`。 |
| 2 | `memory` | 启用 | 启用 | 配置中开启 memory。 |
| 3 | `kb` | 启用 | 启用 | 本地知识索引存在。 |
| 4 | `artifact` | 启用 | 启用 | Artifact store 成功打开。 |
| 5 | `connector` | 启用 | 启用 | Google token 与已授予 scope 决定工具；OAuth/刷新行为不变。 |
| 6 | `media` | 启用 | 禁用 | Media endpoint 配置。 |
| 7 | `browser` | 启用 | 启用 | 本地 Chromium 可用。 |
| 8 | `computer` | 启用 | 禁用 | Linux 或 macOS 后端。 |
| 9 | `mcp` | 启用 | 禁用 | 已配置且可连接的 MCP server。 |

Coding 保留全部已注册 provider（包括宿主扩展），以及 C2 之前的顺序、schema、默认值和审批行为。Personal 仅保留空名称 core provider 和上表启用的具名能力，顺序仍由宿主注册顺序决定；其他宿主注入名称会被禁用。更小的 personal 能力面省去 media client、本地 computer 包装和外部 MCP 会话。`bash` 等 core 工具仍然可用：这一选择不是沙箱或操作系统权限边界。目前所有已注册 connector 共用 `connector` 能力；C2 不增加逐 connector 选择器。

定义与执行都经过 `Registry.Build`；能力报告共用 `ForProfile` 选择和平台门禁，不构建 provider。禁用的 provider 在 `Available` 探测和构建前跳过，不打开 provider 资源，也不记录为降级。启用的 provider 仍执行既有运行时前置条件与降级规则。能力标识表示已启用且平台支持的 provider，并不保证 token 已连接、Chromium 已安装或 server 可达；缺少前置条件时，本轮可以没有相应工具。不可用的 connector 在打开 token store 前跳过。

Personal 的审批默认值仍为 `on-request`，仍拒绝 yolo，省略 Gmail `as_draft` 仍为 `true`；coding 保留 `auto` 和默认发送。草稿与发送的 E/U 仍须人工决定或匹配显式常驻授权。Profile 选择既不创建 standing grant，也不绕过审批。组合在现有 agent loop 内保持确定性。C3 仅改变注册归属；`ForProfile` 完整保留 C2 的启用列表。

## `main@932e14e` 包归属清单

下列清单覆盖 `internal/` 下全部 **29 个顶层目录**，已对照基线树和工作区核实，每个目录只出现一次。这是职责分类，不代表混合包中的所有文件必须一起移动：

- **Core**：保留共享运行时、契约、基础设施，以及本方案未安排拆分的功能。只有上述不变量属于永久约束；这一标签不承诺每个实现文件永远留在原处。
- **First-party plugin (candidate)**：后续候选，继续留在仓库内并静态链接。混合包中的共享契约仍归 Core。
- **Adapter/surface**：把外部界面或协议映射到 Core，不因此获得策略归属，也不会自动成为插件。

| 顶层包 | 分类 | 归属 / 后续边界 |
| --- | --- | --- |
| `internal/agent` | Core | Agent/provider/tool 契约、主循环、执行、审批请求与后果路由。 |
| `internal/app` | Core | Session 用例、run、提示词组装、Registry 宿主、资源归属及共享授权接入；Google/browser/media 注册适配器和 proactive 工厂委托既有实现。 |
| `internal/artifact` | First-party plugin (candidate) | 仅在二进制体积需求成立后，可选拆分版本化产出物和发布支持；保留发布一致性与存储行为。 |
| `internal/bootstrap` | Core | CLI 与 daemon 共用的依赖构造。 |
| `internal/browser` | First-party plugin (candidate) | Chromium/CDP 实现；C3 注册位于 `app/browserplugin.go`，保留导航审批和现有 SSRF 边界。 |
| `internal/cli` | Adapter/surface | 命令/flag 解析；profile 校验委托 Core config，保留现有默认值。 |
| `internal/cliapp` | Adapter/surface | CLI 组装、管理命令、审批呈现与 Core 用例交互。共享审批语义仍归 Core。 |
| `internal/config` | Core | 配置求值、校验、profile 解析和默认值。 |
| `internal/connector` | First-party plugin (candidate) | Connector 实现，优先用子包 `google` 做 C1。将 Core Connector 契约/公共生命周期与 provider 操作分开；不整体搬走 OAuth/token 基础设施。 |
| `internal/contextmgr` | Core | 上下文预算、压缩与会话投影。 |
| `internal/daemon` | Core | 组装根、channel 监督、调度/channel 接线和进程生命周期；保留连接既有实现的 Telegram 工厂适配器。 |
| `internal/filelock` | Core | 共享的跨进程持久化锁。 |
| `internal/kb` | Core | 知识索引构建/搜索/存储支持；C0–C3 未安排拆分。 |
| `internal/library` | First-party plugin (candidate) | 二进制体积需求成立后，可选拆分精选文件集，并保持与 artifact/publish 契约配套。 |
| `internal/mcp` | Adapter/surface | 外部 MCP 连接、schema 与工具桥；仍必须接受 Core 审批。 |
| `internal/media` | First-party plugin (candidate) | 图像/语音/转写客户端和实现；C3 注册位于 `app/mediaplugin.go`。 |
| `internal/memory` | Core | Memory 存储契约、scope、迁移、权限与检索。 |
| `internal/proactive` | First-party plugin (candidate) | 本地投递策略，通过 C3 的 `app.NewProactivePolicy` 构建；不能授予权限或替换 memory 写入门禁。 |
| `internal/provider` | Adapter/surface | 模型 provider 工厂与子包协议客户端；`agent.Provider` 和主循环语义仍是 Core 契约。 |
| `internal/runlog` | Core | 共享的结构化日志与 run context。 |
| `internal/scheduler` | Core | 任务定义、持久化存储、触发循环，以及无人值守执行/审批契约。 |
| `internal/session` | Core | Session repository、JSONL/树契约、租约、冲突检查与恢复。 |
| `internal/skills` | Core | Skill 发现与提示词接入；markdown skill 使用现有工具，不创建另一套插件运行时。 |
| `internal/standingauth` | Core | Grant 持久化、匹配、过期/撤销与放行审计。 |
| `internal/telegram` | First-party plugin (candidate) | Daemon 具体工厂后的 channel API、bot/update 处理与语音接入；daemon 生命周期和共享审批保留在 Core。 |
| `internal/tools` | Core | Coding 工具与共享审批/路径/命令策略；混合其中的 browser/connector/media 包装后续可随能力通过 SPI 拆分。不整体搬走 tools。 |
| `internal/transport` | Core | JSON-RPC、stdio、HTTP/SSE 契约与强制执行。实现上属于传输适配器，但仍是 Core 协议边界的一部分。 |
| `internal/userprofile` | Core | `USER.md` 解析、渲染与存储契约。 |
| `internal/workspace` | Core | Workspace 身份、根目录、Registry 与项目指令查找。 |

此表保留 C0 包分类，注册进度更新至 C3；它不是目录搬迁列表。Google 使用 Connector SPI，browser/media 使用 ToolProvider 适配器，Telegram/proactive 使用具体工厂。`app`、`daemon` 和 `tools` 仍直接 import 实现；C3 不声称完成依赖倒置。

## 分阶段交付与验收

| 阶段 | 范围 | 验收边界 |
| --- | --- | --- |
| **C0 — 文档（已落地）** | 本决策、中文镜像，以及两份架构总览中的链接。 | 仅文档；分类全部 29 个包；无运行时/SPI 代码或 coding 默认值变化。运行 `go vet ./...` 与 `go test ./...`，确认 diff 仅包含预期四份文档。 |
| **C1 — Registry + Tool/Connector SPI（已落地）** | `app.Registry`、Tool/Connector 契约、共用的构建/发现组装，以及静态链接的 Google 注册试点。不做 profile 启用列表过滤。 | 保留工具名/schema/顺序、发现、默认值、前置条件/降级、approval/standing-auth/E/M/U/R、存储路径和资源清理。现有测试及针对 SPI/试点的检查通过。 |
| **C2 — profile 启用列表（已落地）** | `Registry.ForProfile` 过滤构建和能力报告；personal 不启用 media/computer/MCP。 | 定义、执行与对外能力共用选择；不探测或构建禁用 provider。未设置/显式 coding 保留既有能力面与默认值。Profile、平台、资源及审批检查通过。 |
| **C3 — 扩大仓库内拆分（已落地：适配器/工厂）** | 专属 browser/media ToolProvider；保留 daemon Telegram 工厂，增加 app proactive 策略工厂。通用 Channel/Policy SPI 和更广泛提取延后；artifact/library 仍为后期可选项。 | Registry/coding 一致性与前置条件测试通过；既有 Telegram 监督与 proactive 制动测试保持全绿。Core 生命周期、审批、standing-auth/E/M/U/R、coding 默认值及 C2 启用列表不变。不搬迁包。 |
| **C4 — 更后期外部生态（延后）** | 第三方继续 MCP-first。仅针对具体需求重新评估分发/市场。 | 此处不授权 Bun host、npm market、热重载或新的第三方进程内运行时；任何此类提议都需要新的项目决策。 |

C1 先建立保持行为的边界，C2 再改变选择逻辑；C3 通过薄适配器和工厂扩展已验证的接口。移动代码或添加 SPI 本身，不构成引入新 profile 默认值、真实触发器、权限或改变行为的数据迁移的理由。

## 非目标与交付规则

- 不建立空的替代仓库，也不从骨架重新启动产品。
- 不用 Node 重写 Core，不做 Bun 插件宿主、npm 市场或热重载。
- 不整体搬迁 `internal/`，不在 C0 提取运行时代码，不提前声称二进制体积缩小。
- 不削弱 coding 默认值、审批、standing-auth 或后果语义。插件不能绕过这些门禁。
- 不 force-push，不自行 push。C3 提交到 `feat/core-plugin-c3`，由协调者负责 push 与创建 PR。

C3 的审查证据包括 coding 注册/工具顺序与定义一致性、personal 选择和平台门禁不变、browser/media 前置条件缺失与 memoization，以及既有 approval/standing-auth、connector 刷新、browser 清理、Telegram 监督/启动失败和 proactive demo 制动测试，再加上 `go vet ./...` 与 `go test ./...`。PR 正文记录改动内容、动机、取舍和实际验证结果。通用 channel/policy SPI 与更广泛提取仍延后；market/Bun 工作不在本次交付中。
