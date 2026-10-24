# Personal Agent：定义与底线（P0）

[English](personal-agent.md) | 简体中文

gg → Personal Agent 改造计划的 P0「定义与底线」定义后果语言，记录当前审批覆盖。本阶段只改文档；下文标为目标的策略属于后续工作。

## 个人代理与编码代理

今天 gg 主要还是终端 **coding agent（编码代理）**，定位见 [README](../README.zh-CN.md)。[个人层](architecture.zh-CN.md#个人层画像--记忆)已经接入共享运行时：`USER.md` 提供用户画像，memory 提供持久上下文。有了这些上下文，并不表示当前已经有独立的个人身份或更严格的默认策略。

目标是继续共享会话、provider、工具和审批运行时。P3 再分叉**身份与默认策略**：coding 默认保持适合编码工作的体验，personal 默认对私人、可逆工作之外的后果更严格。这是身份和策略的分叉，不是再造一套运行时。

P0 的成功标准：

- P1 能在 `agent.ApprovalRequest` 的 `risk` 旁增加后果字段，无需重新争论下文定义。
- 审批清单与 `origin/main` 基线一致，包含有意豁免和个人模式缺口。
- 本次改动不改变运行时行为：审批模式、CLI 参数、`DefaultApprover`、runner 的失败关闭逻辑、工具默认值、`ApprovalDescriber` 实现，以及断言现有审批行为的测试都保持原样。

## 四个后果维度

根据一次具体调用的参数、目的地、账户和上下文判断后果。工具名用于举例和盘点，不是路由的首要规则。同一个工具在不同调用中可能产生不同后果。

| 维度 | 定义 | 边界示例 |
| --- | --- | --- |
| **R — 可逆（Reversible）** | 整个效果能否低成本、在本地撤销，且不被外部当事人察觉？若已经发生披露、打扰、花费或身份归属，仅恢复字节还不够。 | 隔离的本地草稿且旧内容可恢复，可以是可逆的。发出消息、产生费用、终止有未保存状态的进程，通常不可逆。存在删除或取消操作，不等于原始效果已经撤销。 |
| **E — 对外可见（Externally visible）** | 是否在机器之外留下他人（包括服务运营方）可观察的痕迹？不要求公开可见。个人边界也将 OS 通知纳入此维度：即使不联网，它也会把内容展示到代理私人工作上下文之外。 | 发送、发帖、打开第三方 URL、上传音频、创建日历邀请、展示 OS 通知。只读网络请求同样可能留下服务方可见的痕迹。 |
| **M — 花钱或改变权限（Spends money or changes permissions）** | 是否产生普通聊天之外的费用，或改变权限和访问能力？两者都足以命中本维度，即使没有创建公开内容。 | 计费的图片、语音、转写、embedding API 调用；OAuth 或 scope 变更；授予访问权；破坏性的权限变更。免费端点只消除了花费，不会消除其他后果。 |
| **U — 以用户名义行动（Acts in the user's name）** | 是否发送、发布或创建可归属于用户的内容，包括私人账户内的内容？关注的是身份代表性，不只是带凭据读取。 | 从用户账户发邮件、在用户日历中创建事件、以用户身份发帖。经过身份验证的搜索本身不等于命中此维度。 |

四个维度彼此独立：本地权限变更可以命中 M 而不命中 E；创建私人日历事件属于 U，也会在服务方留下 E 痕迹；本地草稿可以可逆，发送后的版本则不可逆。可逆不等于已经获得授权。只读也不等于本地、免费或私密。

这些维度描述工具效果。本地读取的结果也可能作为对话上下文进入配置的聊天 provider；这条共享运行时数据流仍然重要，但与工具自行发起外部请求是不同环节。即使没有直接的 E/M/U 效果，剪贴板等敏感读取的现有审批仍有意义。

计划的路由原则是按这些属性判断，而不是枚举动作名。确定性证据和显式授权划定边界。模型只在**可逆／不可逆**判断的空白处补位，不能凭空推导授权、豁免 E/M/U 后果或扩大授权范围。证据缺失仍是未决，不会变成许可。P0 只建立词汇，不增加后果字段、路由逻辑或新的审批默认值。

## 常驻授权边界（P2）

常驻授权（standing authorization）必须显式包含三部分：

| 部分 | 必须表达的含义 |
| --- | --- |
| **主体（Subject）** | 谁可以行动，以及代表哪个用户或账户。 |
| **范围（Scope）** | 允许涉及哪些资源、目的地和效果，以及收件人、花费或允许的权限变更等限制。宽泛目标本身不是授权范围。 |
| **有效期（TTL）** | 明确的有效时间窗口或到期时间。不能推导无限期授权，也不能复用已过期授权。 |

`USER.md` 中的偏好、记忆中的习惯、OAuth 连接或上一次批准，本身都不构成常驻授权。P2 再实现常驻授权；P0 不创建授权，也不改变现有审批方式。

## 当前审批清单

基线：`origin/main` 的 `8776b83`（#53），也是 `docs/personal-agent-p0` 的分支基点。清单对照了[内置工具实现](../internal/tools/)、[工具注册表](../internal/app/toolregistry.go)和 [MCP 桥接](../internal/mcp/tool.go)。覆盖 **30 个内置工具：15 个实现、15 个未实现 `agent.ApprovalDescriber`**，另加全部动态注册的 MCP 工具。实际注册取决于配置、已授予的 scope、provider 可用性和平台支持，并非每个会话都会暴露全部工具。

[`agent.ApprovalDescriber`](../internal/agent/types.go) 是包含 `ApprovalRequest` 方法的接口。“是”表示工具向 [runner](../internal/agent/runner.go)提供审批请求，不表示每次调用都会弹出人工确认。现有策略可以允许或拒绝请求，`write`/`edit` 还有狭窄的预批准豁免。“否”表示该接口不拦截此工具，不表示工具没有后果。

表中的 R/E/M/U 只是文档缩写，不是拟定的 API 字段名。R 描述整个效果是否可逆；“视情况”需要具体调用的证据。E/M/U 表示典型或条件性直接后果，不是完整策略。同步目的地、应用行为和远程端点都可能改变判断。

### 已实现 ApprovalDescriber 的工具

| 工具 | ApprovalDescriber？ | 典型后果维度 | 说明／个人模式缺口？ |
| --- | --- | --- | --- |
| [`write`](../internal/tools/write.go) | 是 | R：取决于能否恢复被覆盖内容；通常是本地操作。 | 创建／覆盖工作区文件。真实 `.gg/agent/` 目录中的写入预先放行；本阶段保留此豁免。 |
| [`edit`](../internal/tools/edit.go) | 是 | R：取决于能否恢复旧内容；通常是本地操作。 | 精确替换并展示修改前后预览。具有同样的真实 `.gg/agent/` 目录预批准豁免。 |
| [`bash`](../internal/tools/bash.go) | 是 | R/E/M/U：取决于命令和目标。 | 现有分类器和可选风险检查填写 `risk`/`riskReason`。shell 命令可能涉及任意维度。 |
| [`browser_navigate`](../internal/tools/browser.go) | 是 | R：第三方请求痕迹不可逆；E。 | 初始 URL 经 SSRF 检查，导航走审批。重定向和子资源限制仍见架构文档。 |
| [`gmail_send`](../internal/tools/connectors.go) | 是 | R：发送后不可逆；E、U。 | 从用户 Gmail 账户发送。删除本地副本不能撤回投递或身份归属。 |
| [`calendar_create`](../internal/tools/connectors.go) | 是 | R：外部痕迹不可逆；E、U。 | 以用户账户在主日历创建事件。当前参数不包含参会人；发送邀请是边界示例，不代表此工具具备该能力。 |
| [`artifact_create`](../internal/tools/artifact.go) | 是 | R：本地创建可移除；无直接 E/M/U。 | 写入工作区之外的本地 artifact store，不会发布到外部服务。 |
| [`artifact_edit`](../internal/tools/artifact.go) | 是 | R：保留的版本可恢复旧内容；无直接 E/M/U。 | 追加完整本地版本。发布到 library 是独立的应用／CLI 操作，不是此工具。 |
| [`image_generate`](../internal/tools/media.go) | 是 | R：远程披露或已产生费用不可逆；远程时 E，计费时 M。 | 向媒体端点发送 prompt，将图片保存在本地。删除输出不会撤销费用。 |
| [`tts`](../internal/tools/media.go) | 是 | R：远程披露或已产生费用不可逆；远程时 E，计费时 M。 | 向语音端点发送文本，将音频保存在本地。 |
| [`stt`](../internal/tools/media.go) | 是 | R：远程披露或已产生费用不可逆；远程时 E，计费时 M。 | 上传本地音频进行转写，不只是读取本地文件。 |
| [`process_kill`](../internal/tools/computer.go) | 是 | R：丢失的进程状态通常不可逆；联网服务可能涉及 E。 | 发送 SIGTERM 或 SIGKILL。重启不一定恢复状态或撤销中断。 |
| [`open`](../internal/tools/computer.go) | 是 | R：视情况；第三方 URL 涉及 E；其他维度取决于目标／应用。 | 将文件或 URL 交给默认应用。应判断后续效果，不能只看本地启动动作。 |
| [`clipboard_read`](../internal/tools/computer.go) | 是 | R：本地读取；无直接 E/M/U。 | 剪贴板可能有敏感内容，因此有意加审批。后续披露不能靠清空剪贴板撤销。 |
| [`clipboard_write`](../internal/tools/computer.go) | 是 | R：取决于能否恢复被替换内容；同步或外部消费时可能涉及 E。 | 替换系统剪贴板文本。本地写入可能影响其他应用或设备。 |
| [`mcp_<server>_<tool>`](../internal/mcp/tool.go) | 是，全部 MCP 工具 | R/E/M/U：取决于 server、操作和参数。 | 每个桥接的 MCP 调用都过审批，包括自称只读的远程工具。审批覆盖完整，但桥接层目前尚未提供后果分类。 |

### 未实现 ApprovalDescriber 的工具

| 工具 | ApprovalDescriber？ | 典型后果维度 | 说明／个人模式缺口？ |
| --- | --- | --- | --- |
| [`memory_add`](../internal/tools/memory_add.go) | 否 | R：取决于能否在复用前移除条目；属于持久本地写入。 | **明确的个人模式缺口。** 追加工作区／全局记忆，可影响后续轮次。[`TestMemoryAddToolDoesNotRequireApproval`](../internal/tools/memory_add_test.go) 明确断言它不实现此接口。P1 至少会在个人模式下加审批；P0 保留实现与测试预期。 |
| [`memory_search`](../internal/tools/memory_search.go) | 否 | R：本地读取；无直接 E/M/U。 | 跨工作区／全局记忆做只读关键词检索。`memory_add_test.go` 同样断言其无需审批。 |
| [`read`](../internal/tools/read.go) | 否 | R：本地读取；无直接 E/M/U。 | 读取工作区文件或配置的额外只读根目录。敏感内容仍会进入对话。 |
| [`list`](../internal/tools/list.go) | 否 | R：本地读取；无直接 E/M/U。 | 列出工作区目录项。 |
| [`grep`](../internal/tools/grep.go) | 否 | R：本地读取；无直接 E/M/U。 | 搜索工作区文本文件。 |
| [`browser_read`](../internal/tools/browser.go) | 否 | R：读取当前页面；无新增的直接 E/M/U。 | 有意豁免：导航走审批，随后读取该页面不再发起导航。 |
| [`browser_screenshot`](../internal/tools/browser.go) | 否 | R：可移除的本地截图；无新增的直接 E/M/U。 | 沿用同一导航边界；将 PNG 保存在本地。虽然不导航，但确实会写文件。 |
| [`gmail_search`](../internal/tools/connectors.go) | 否 | R：服务方请求痕迹不可逆；E。 | 只读 Gmail API 搜索；第三方访问并非纯本地工作。 |
| [`gmail_read`](../internal/tools/connectors.go) | 否 | R：服务方请求痕迹不可逆；E。 | 经 Gmail API 读取邮件，不会以用户身份发送。 |
| [`calendar_agenda`](../internal/tools/connectors.go) | 否 | R：服务方请求痕迹不可逆；E。 | 经 Calendar API 读取近期事件，不创建事件。 |
| [`computer_info`](../internal/tools/computer.go) | 否 | R：本地读取；无直接 E/M/U。 | 读取 OS、主机、CPU、内存和磁盘信息。 |
| [`process_list`](../internal/tools/computer.go) | 否 | R：本地读取；无直接 E/M/U。 | 查看运行中的进程，不会终止它们。 |
| [`notify`](../internal/tools/computer.go) | 否 | R：已展示／打扰后不可逆；E（OS 通知）。 | **潜在的个人模式缺口。** 当前视作无害且不走审批；内容在 OS 界面上可见，也可能打扰用户。P0 只记录缺口，不加审批。 |
| [`subagent`](../internal/tools/subagent.go) | 否 | R：取决于 provider 痕迹；远程聊天 provider 涉及 E。 | 子工具仅有 `read`/`list`/`grep`。会增加普通聊天调用，仅普通聊天费用不算 M。子工具只读不代表处理全在本地完成。 |
| [`kb_search`](../internal/tools/kb_search.go) | 否 | R：视情况；远程 embedding 端点涉及 E，计费时涉及 M。 | 本地索引只读，但会经配置的 API 为查询生成 embedding。披露／花费是另一个个人模式待评估点；“搜索”不保证没有外部后果。 |

## 现有策略与阶段边界

当前运行时策略见架构文档的[安全模型](architecture.zh-CN.md#安全模型)和[审批模式](architecture.zh-CN.md#审批模式)。其中包括 #53 引入的 nil approver 通过 `agent.DefaultApprover` 对 `dangerous`/`confirm` 风险失败关闭的处理；P0 保持原样。

P1 会在现有 `risk` 语言旁增加后果元数据，并处理覆盖缺口，包括个人模式下的记忆写入。P2 增加显式常驻授权。P3 在共享运行时之上分叉身份与默认策略。本文档不实现这些机制、更严格的默认值或新的审批门槛。
