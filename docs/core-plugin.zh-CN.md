# Core + 第一方插件

[English](core-plugin.md)

本文是已批准 Core+plugin 方案的 C0 架构决策，以 `main@932e14e` 为基线。C0 只修改文档：不实现 Registry 或 SPI，不移动包，不改变工具集合、profile 默认值、审批策略或运行时行为。[架构总览](architecture.zh-CN.md)描述当前系统；下文各阶段描述后续工作。

## 目标与边界

保留一个 Go Core，并提供稳定的服务提供者接口（SPI）。第一方插件继续作为仓库内的 Go 包编译进同一个二进制，由 Core 组装。Profile 在这个运行时内选择有序的启用列表和默认值。第三方能力继续优先通过 MCP 接入，沿用外部进程或 HTTP 边界。

目标是明确能力归属与组装方式，同时保持 coding 产品体验。插件边界不要求独立进程、独立安装包或更小的二进制。静态链接让第一方能力与 Core 共用构建、审查和发布生命周期；后续 profile 过滤缩小的是启用的能力面，不是链接后的二进制体积。

后续阶段的第一方候选包括：

- **Connectors**：优先用 `internal/connector/google` 作为 C1 试点。
- **Browser** 与 **media** 能力实现。
- **Telegram channel** 与 **proactive 投递策略**实现。
- **Artifact/library**：仅在缩小二进制成为具体需求后，作为更后期的可选拆分。单靠启用列表无法移除已链接的代码。

C0 只列出候选，不授权批量移动目录，也不定义最终 SPI 类型。

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

第一方插件以 gg 的 OS 权限执行。静态链接和 SPI 提供归属边界，不提供沙箱。实现必须保留现有审批描述，不能绕过 approval 或 standing-auth。Proactive 决定何时展示摘要属于投递策略，不代表获准发送、通知或写入记忆。MCP 工具继续保留现有的全量审批门禁及已记录的分类局限。

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

## SPI 与包生命周期方向

当前切入点是 [`app.ToolProvider`](../internal/app/toolregistry.go)：具名工具 builder、可选的平台可用性检查、确定的注册顺序，以及通过 `Options.ToolProviders` 供宿主/测试注入的机制。[`conversationTools`](../internal/app/toolset.go) 管理每个会话的资源和清理；工具定义与执行已共用这条构建路径。这是现有实现的切入点，还不是稳定的插件 SPI。

C1 应从这些行为提炼 Registry 和 Tool/Connector SPI。准确的包路径、导出的 Go 类型和版本协商机制，留给 C1 设计与实现审查。预期归属契约如下：

| 生命周期领域 | Core / 宿主 | 第一方实现 |
| --- | --- | --- |
| 注册与兼容性 | 拥有标识符、确定的组装、校验和 SPI 兼容契约。 | 通过约定 SPI 注册能力元数据与实现。 |
| 配置与选择 | 解析 profile/config，确定启用能力和默认值。 | 使用经过校验的能力配置，保留现有连接/scopes 行为。 |
| 构建与发现 | 工具定义、执行和对外能力共用选定的 Registry。保留平台门禁，以及预期禁用与运行故障的区分。 | 构建工具/客户端，通过契约报告可用性或运行故障。 |
| 执行与信任 | 保留 agent 分发、审批、standing-auth、后果解释与审计。 | 描述操作后果，仅通过 Core 的分发和审批路径执行。 |
| 资源生命周期 | 拥有 session/daemon 生命周期，在关闭或淘汰时调用清理。 | 通过宿主生命周期释放所拥有的客户端、连接和其他资源。 |
| 持久化与迁移 | 保留共享存储契约，协调兼容的启动/迁移。 | 在该生命周期内负责能力专属数据及迁移逻辑；迁移时保持既有路径和权限。 |

优先选择 Google 试点时，connector API 操作及其工具包装属于候选实现。Core 保留 Connector SPI、config/profile 选择、授权和公共生命周期规则。OAuth scopes 和 token 不能替代 gg 审批或 standing grant。C1 必须保留工具名、schema、顺序、默认值、token 路径、刷新行为、错误与清理语义；抽出接口本身不代表可以改变行为。

第一方包随 gg release 发布与迁移。本方案不引入独立安装器、npm 依赖解析器、动态 Go 插件加载器或热重载生命周期。稳定 SPI 是持续维护的源码级契约，不承诺二进制 ABI，也不提供第三方进程内加载 API。

## Profile 与 coding 兼容性

在 `932e14e`，[`config.ParseProfile`](../internal/config/config.go) 接受 `coding` 和 `personal`；未设置或空值选择 coding。[现有 personal profile](personal-agent.zh-CN.md#选择-profilep3) 改变 CLI 身份与默认值，包括审批模式和 Gmail 草稿偏好，但**尚未**对工具/connector Registry 实现 profile 启用列表过滤。Daemon、Web/Electron、Telegram 和 daemon 定时轮次继续保持已记录的 coding profile 行为。

C2 将让 `--profile` 同时选择启用的工具/connector 集合与默认值。Core 求值有序能力列表，应用前置条件/平台检查，并让工具定义、执行与能力报告统一使用结果集合。禁用的 provider 不应被构建或打开资源。组合顺序必须确定；profile 不能安装代码、创建授权、覆盖 Core 不变量或引入自己的 agent loop。

未设置/空值和显式 `coding` 路径必须保留现有 coding 工具、顺序、默认值和审批行为。Personal 过滤和其他选择入口需要明确的 C2 范围及检查；C0 不承诺新 CLI flag、配置语法或 daemon selector。Personal 现有的更严格默认值继续保留。Profile 启用能力永远不代表已获准使用相应工具。

## `main@932e14e` 包归属清单

下列清单覆盖 `internal/` 下全部 **29 个顶层目录**，已对照基线树和工作区核实，每个目录只出现一次。这是职责分类，不代表混合包中的所有文件必须一起移动：

- **Core**：保留共享运行时、契约、基础设施，以及本方案未安排拆分的功能。只有上述不变量属于永久约束；这一标签不承诺每个实现文件永远留在原处。
- **First-party plugin (candidate)**：后续候选，继续留在仓库内并静态链接。混合包中的共享契约仍归 Core。
- **Adapter/surface**：把外部界面或协议映射到 Core，不因此获得策略归属，也不会自动成为插件。

| 顶层包 | 分类 | 归属 / 后续边界 |
| --- | --- | --- |
| `internal/agent` | Core | Agent/provider/tool 契约、主循环、执行、审批请求与后果路由。 |
| `internal/app` | Core | Session 用例、run、提示词组装、Registry 宿主、资源归属及共享授权接入；候选能力专属 builder 后续可通过 SPI 委托。 |
| `internal/artifact` | First-party plugin (candidate) | 仅在二进制体积需求成立后，可选拆分版本化产出物和发布支持；保留发布一致性与存储行为。 |
| `internal/bootstrap` | Core | CLI 与 daemon 共用的依赖构造。 |
| `internal/browser` | First-party plugin (candidate) | Chromium/CDP 能力实现；保留导航审批和现有 SSRF 边界。 |
| `internal/cli` | Adapter/surface | 命令/flag 解析；profile 校验委托 Core config，保留现有默认值。 |
| `internal/cliapp` | Adapter/surface | CLI 组装、管理命令、审批呈现与 Core 用例交互。共享审批语义仍归 Core。 |
| `internal/config` | Core | 配置求值、校验、profile 解析和默认值。 |
| `internal/connector` | First-party plugin (candidate) | Connector 实现，优先用子包 `google` 做 C1。将 Core Connector 契约/公共生命周期与 provider 操作分开；不整体搬走 OAuth/token 基础设施。 |
| `internal/contextmgr` | Core | 上下文预算、压缩与会话投影。 |
| `internal/daemon` | Core | 组装根、channel 监督、调度/channel 接线和进程生命周期；后续委托候选实现。 |
| `internal/filelock` | Core | 共享的跨进程持久化锁。 |
| `internal/kb` | Core | 知识索引构建/搜索/存储支持；C0–C3 未安排拆分。 |
| `internal/library` | First-party plugin (candidate) | 二进制体积需求成立后，可选拆分精选文件集，并保持与 artifact/publish 契约配套。 |
| `internal/mcp` | Adapter/surface | 外部 MCP 连接、schema 与工具桥；仍必须接受 Core 审批。 |
| `internal/media` | First-party plugin (candidate) | 图像/语音/转写客户端和能力实现。 |
| `internal/memory` | Core | Memory 存储契约、scope、迁移、权限与检索。 |
| `internal/proactive` | First-party plugin (candidate) | 本地投递策略实现；不能授予权限或替换 memory 写入门禁。 |
| `internal/provider` | Adapter/surface | 模型 provider 工厂与子包协议客户端；`agent.Provider` 和主循环语义仍是 Core 契约。 |
| `internal/runlog` | Core | 共享的结构化日志与 run context。 |
| `internal/scheduler` | Core | 任务定义、持久化存储、触发循环，以及无人值守执行/审批契约。 |
| `internal/session` | Core | Session repository、JSONL/树契约、租约、冲突检查与恢复。 |
| `internal/skills` | Core | Skill 发现与提示词接入；markdown skill 使用现有工具，不创建另一套插件运行时。 |
| `internal/standingauth` | Core | Grant 持久化、匹配、过期/撤销与放行审计。 |
| `internal/telegram` | First-party plugin (candidate) | Channel API、bot/update 处理与语音接入；daemon 生命周期和共享审批保留在 Core。 |
| `internal/tools` | Core | Coding 工具与共享审批/路径/命令策略；混合其中的 browser/connector/media 包装后续可随能力通过 SPI 拆分。不整体搬走 tools。 |
| `internal/transport` | Core | JSON-RPC、stdio、HTTP/SSE 契约与强制执行。实现上属于传输适配器，但仍是 Core 协议边界的一部分。 |
| `internal/userprofile` | Core | `USER.md` 解析、渲染与存储契约。 |
| `internal/workspace` | Core | Workspace 身份、根目录、Registry 与项目指令查找。 |

明确保留基线现状：`app` 和 `tools` 目前直接 import 多个候选实现。清单分配未来归属，不表示这些依赖已经完成倒置。

## 分阶段交付与验收

| 阶段 | 范围 | 验收边界 |
| --- | --- | --- |
| **C0 — 文档** | 本决策、中文镜像，以及两份架构总览中的链接。 | 仅文档；分类全部 29 个包；无运行时/SPI 代码或 coding 默认值变化。运行 `go vet ./...` 与 `go test ./...`，确认 diff 仅包含预期四份文档。 |
| **C1 — Registry + Tool/Connector SPI** | 引入共享契约，零行为变化迁移现有注册。包含一个第一方示例，优先 Google，仍静态链接。 | 保留工具名/schema/顺序、发现、默认值、前置条件/降级、approval/standing-auth/E/M/U/R、存储路径和资源清理。现有测试及针对 SPI/试点的检查通过。 |
| **C2 — profile 启用列表** | 让 `--profile` 通过 Core 组装真正裁剪启用的工具/connector。 | 定义、执行与对外能力一致；不构建禁用 provider。未设置/显式 coding 保留既有能力面与默认值。验证 profile 过滤及授权不变。 |
| **C3 — 扩大仓库内拆分** | 将已验证的接口扩展到 browser/media，以及 Telegram/proactive 等 channel/策略实现。Artifact/library 仍为后期可选项。 | 增量迁移、测试全绿、Core 归属稳定，保留 channel 监督、审批与 coding 行为。不对 `internal/` 做大爆炸搬迁。 |
| **C4 — 更后期外部生态** | 第三方继续 MCP-first。仅针对具体需求重新评估分发/市场。 | 此处不授权 Bun host、npm market、热重载或新的第三方进程内运行时；任何此类提议都需要新的项目决策。 |

C1 先建立保持行为的边界，C2 再改变选择逻辑；只有接口得到验证后，C3 才扩大拆分。移动代码或添加 SPI 本身，不构成引入新 profile 默认值、真实触发器、权限或改变行为的数据迁移的理由。

## 非目标与交付规则

- 不建立空的替代仓库，也不从骨架重新启动产品。
- 不用 Node 重写 Core，不做 Bun 插件宿主、npm 市场或热重载。
- 不整体搬迁 `internal/`，不在 C0 提取运行时代码，不提前声称二进制体积缩小。
- 不削弱 coding 默认值、审批、standing-auth 或后果语义。插件不能绕过这些门禁。
- 不 force-push。C0 提交到 `feat/core-plugin-c0`，由协调者负责 push 与创建 PR。

C0 的审查证据是四文件文档 diff，以及 `go vet ./...` 和 `go test ./...`。Go 检查用于发现意外的构建/测试漂移，不能验证尚未实现的 SPI。PR 正文记录改动内容、动机、取舍和实际验证结果。
