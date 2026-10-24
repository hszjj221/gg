# Personal Agent：主动性策略与自用验证路径（P4 首个切片）

[English](personal-agent.md) | 简体中文

P0 定义后果语言与审批基线。P1 增加后果路由、记忆写入审批及 Gmail 草稿。P2 增加显式常驻授权与审计匹配。P3 增加独立基础身份与更严格默认策略的个人 profile。P4 首个切片增加确定性的主动性策略，以及验证这些刹车仍有效的可运行假数据自用路径。默认 `gg` CLI 仍以编码为主。

## 个人代理与编码代理

gg 主要是终端 **coding agent（编码代理）**，定位见 [README](../README.zh-CN.md)。两种 profile 共用[个人层](architecture.zh-CN.md#个人层画像--记忆)：`USER.md` 提供用户画像，memory 提供持久上下文。运行时 profile 选择助手的身份与默认策略，与这些用户上下文分别处理。

P3 共享会话、provider、工具、审批和常驻授权，同时分离**身份与默认策略**。个人面以用户的个人助理为基础身份，优先私人、可逆工作和草稿。在仓库中仍遵循项目 `AGENTS.md`；选定基础指令后，`USER.md`、memory、项目指令及 skills 的组装顺序保持不变。

P1 扩展现有的 `ApprovalDescriber` → `ApprovalRequest` → `Approver` 路径，保留 `Risk` / `RiskReason`、普通调用的审批模式及 bash 分类器。路由依据后果属性，不枚举工具名称。

## 选择 profile（P3）

```sh
gg                              # coding，保持现有行为
gg --profile coding             # 显式选择编码 profile
gg --profile personal           # 个人助理面
gg --profile personal -p '准备一封回复草稿'
```

`--profile` 仅接受 `coding` 和 `personal`；未设置或空值均为 `coding`。选择仅作用于本次调用，包括恢复会话。P3 不增加持久化配置默认值或第二个二进制。

C2 还会过滤启用能力：personal 保留 core 工具、memory、KB、artifact、Google connector 和 browser，不启用 media、computer 与 MCP provider。Coding 保留既有完整集合。前置条件与宿主注入行为见[有序 profile 启用列表](core-plugin.zh-CN.md#profile-与-coding-兼容性)。下方草稿与审批默认值不变；启用能力不会授予授权。

| 默认值／策略 | Coding | Personal |
| --- | --- | --- |
| 基础身份 | 在用户项目中工作的编码代理 | 用户的个人助理 |
| 审批模式 | `auto` | `on-request` |
| `--yolo`／`--approval yolo` | 保持现有行为 | 拒绝，并提示使用 standing-auth |
| Gmail 省略 `as_draft` | `false`：发送 | `true`：创建草稿，不发送 |

Personal 默认的 `on-request` 要求 stdin 和 stderr 都是终端，否则 CLI 在调用模型前退出。有终端时，普通需审批调用会询问；现有只读和 agent 目录豁免保留。显式 `--approval auto`、`on-request` 或 `never` 可覆盖默认值。`never` 保留普通调用不询问直接放行的历史行为，但不授权明确的 E/M/U 或 dangerous/confirm 调用。无人值守配合常驻授权时，显式选择 `--approval auto`：未匹配的需审批调用会被拒绝。两种 profile 的明确 E/M/U 仍须人工确认或匹配常驻授权；授权始终不能豁免 dangerous/confirm 风险。

Personal Gmail 调用省略 `as_draft` 时创建真实草稿。显式 `as_draft=false` 仍可请求发送，经过相同的 E/U 审批路径。预览与结果反映实际动作。草稿与发送均为 E/U，不会自动获准；草稿失败绝不回退到发送。常驻授权的参数限制仍匹配原始显式参数：即使在 personal 中，限制 `as_draft=true` 的授权也要求调用明确包含它。

选定的 `Config.Profile` 传入共享提示词与工具注册表，包括手动 `gg --profile personal job run ...` 轮次。手动任务保留任务审批策略（`--allow-all`），不使用交互 CLI 模式；profile 不存入任务定义。`ggd`、Web/Electron、Telegram 及 daemon 定时任务保持现有 coding profile 和审批策略，入口选择器留待后续。下方 P4 demo 由用户显式调用 CLI，与定时 agent 轮次分开。

## 主动性策略（P4 首个切片）

[`internal/proactive.Policy.Decide`](../internal/proactive/policy.go) 是纯函数：不调用模型、不做 I/O、不读取时钟、不推导授权。输入包括运行时 profile、场景（`email_summary`、`calendar_reminder`、`standing_auth_check`）、传入的本地时间、显式的稍后记住请求，以及拟议通知或工具审批请求。本切片在 demo 中调用它，不向普通 agent 轮次或 daemon 添加自动触发器。

| 决定 | 含义 | 本切片中的动作 |
| --- | --- | --- |
| **Summarize（摘要）** | 现在提供简短摘要有帮助。 | 将本地摘要输出到 stdout，不向外投递。 |
| **Quiet（安静）** | 不打扰。 | 不展示正文、不通知、不发送消息；demo 诊断可记录决定。 |
| **Note-only（仅记录）** | 留待以后，不在此刻展示内容。 | 写入私有本地候选文件，不插入持久记忆；之后使用 `memory_add` 仍须经过现有审批。 |

默认值与优先顺序：

- Coding（包括未设置 profile）返回 Quiet／禁用。未知 profile 和场景也保持安静。
- Personal 的邮件／日历摘要可在本地 Summarize。安静时段默认关闭；`QuietHours="22:00-07:00"` 使用传入时间的时区，包含起点、不含终点，支持跨午夜。起止相同或格式错误会被拒绝；无效策略输入保持 Quiet。
- 显式的稍后记住请求选择 Note-only，包括在安静时段内。它不会改变拟议工具或通知的授权要求。
- OS `notify` 关闭，每次运行固定最多 **零条通知**，本切片不提供开启选项。拟议通知返回 Quiet；OS 通知按 E 处理。现有 `notify` 工具仍未实现 `ApprovalDescriber`（见下方 P0 清单／P1 覆盖缺口）；本策略不改变它在普通轮次中的可用性。
- 拟议工具动作返回 Quiet，必须走现有 Approver／常驻授权路径。明确的 E/M/U 始终须人工确认或匹配常驻授权。`gmail_send`（包括真实 Gmail 草稿）和 `calendar_create` 保留 P1–P3 门禁。Summarize 与 Note-only 均不授权工具调用，未知工具元数据也不是许可。

## 运行假数据自用验证

无需模型 key、Google 连接、真实邮箱或终端：

```sh
gg --profile personal proactive demo
# 在 checkout 中：go run ./cmd/gg --profile personal proactive demo

# 在模拟的本地安静时段内隐藏两种摘要正文：
gg --profile personal proactive demo --quiet-hours 22:00-07:00 --at 23:00

# 改为捕获私有候选，即使当前处于安静时段：
gg --profile personal proactive demo --remember-later --quiet-hours 22:00-07:00 --at 23:00
```

默认模拟时间是机器本地时区的 10:00（`--at HH:MM` 可修改）。假收件箱片段生成简短摘要；30 分钟后的假事件在策略允许时生成本地提醒，不调用 `calendar_create`。Note-only 将权限为 0600 的候选文件写入新建的 0700 `~/.gg/proactive/demo-*` 目录，通过 `Config.HomeDir` 定位。仅输出候选路径／决定元数据，不输出内容。demo 目录保留供检查，之后可删除；它们从不作为记忆加载。

常驻授权场景使用真实 `GmailSendTool.ApprovalRequest` 元数据（personal 省略 `as_draft` 为草稿，显式 `false` 为发送）、共享 `standingauth.Approver` 以及无人可应答的 CLI 审批器。demo 目录内全新的空授权存储将检查与真实授权隔离。两个尝试均须被拒绝，否则命令失败。它从不执行这两个工具动作，也不创建授权。成功退出码为 0，并输出 **“brakes held”**。Coding 下的 `gg proactive demo` 输出 Quiet／禁用，不创建 demo 状态。demo 在会话、工作区和记忆初始化前返回，从不构造真实连接器／模型客户端。

策略和 CLI 测试覆盖本地摘要可见性、Note-only 捕获、安静时段、通知关闭，以及真实 gg home 存在匹配授权时 demo 中的草稿／发送仍被拒绝。现有常驻授权测试覆盖匹配授权放行与审计。CI 无需真实凭据。

### 可选的真实只读冒烟

已配置模型且已有 Google 连接（`gg connect google`）时，在终端运行：

```sh
gg --profile personal -p '使用 gmail_search/gmail_read 汇总最近未读邮件，用 calendar_agenda 查看今天日程。摘要只留在此会话中，不发送、不创建草稿或事件、不通知、不添加记忆。'
```

这里使用正常的 personal-profile agent 路径，不是假数据 demo，也不由策略自动触发。此步骤可选，不纳入 CI。Gmail／Calendar 读取会访问 Google，结果进入配置的聊天 provider 上下文，并非纯本地处理。发送、真实 Gmail 草稿和事件创建仍须人工审批或匹配显式授权。凭据仅保存在现有本地 provider 配置／环境变量中。

## P4 范围与停止条件

| 计划项 | 本切片已交付 | 延后 |
| --- | --- | --- |
| 主动触发策略 | 确定性的 Summarize／Quiet／Note-only 决定、仅 personal 启用的默认值、安静时段、通知抑制与后果边界。 | 自动发现触发条件、持续运行的摘要调度。 |
| 首位用户自用验证 | 可发现的 CLI 假邮件摘要、日历提醒与常驻授权拒绝场景，配套测试及可选真实只读操作说明。 | Daemon／Telegram 主动投递、为 `notify` 实现 `ApprovalDescriber`。 |
| 停止条件指针 | 保持编码优先默认值和共享运行时。 | 物理拆分仓库。 |

**计划停止条件：** 仅当 personal 默认策略**系统性损害 coding**，出现审批噪声、工具循环或 README 人设漂移的证据时，才考虑物理拆分仓库。本切片不拆仓库、不重写 agent 循环、不增加第二运行时，也不扩大 MCP／bash 后果分类。现有 `gg job`／`internal/scheduler` 继续承担调度；不新增调度器，也不将 demo 接入调度。

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

计划的路由原则是按这些属性判断，而不是枚举动作名。确定性证据和显式授权划定边界。模型只在**可逆／不可逆**判断的空白处补位，不能凭空推导授权、豁免 E/M/U 后果或扩大授权范围。证据缺失仍是未决，不会变成许可。P1 实现下文明确断言的 E/M/U 边界；未分类调用继续使用现有的风险策略。

## P1 运行时行为

这些规则仍是常驻授权未匹配时的回退策略。

[`agent.ApprovalRequest`](../internal/agent/types.go) 在 `Risk` / `RiskReason` 旁增加四个可选 `*bool` 字段：

| 维度 | Go 字段 | JSON 键 |
| --- | --- | --- |
| R | `Reversible` | `reversible` |
| E | `ExternallyVisible` | `externallyVisible` |
| M | `SpendsMoneyOrChangesPermissions` | `spendsMoneyOrChangesPermissions` |
| U | `ActsInUserName` | `actsInUserName` |

`nil`（JSON 省略）表示未知；显式 `false` 表示已经判断且不存在该维度。未知不是安全或授权的声明。`RequiresHumanConfirm()` 判断 E、M、U 是否有任一显式为 true；此检查先于只读快捷放行。R 本身不授予权限，也不覆盖风险判断。

- 交互式 `runApprover` 继续向用户请求审批。CLI 在终端中对明确的 E/M/U 弹出确认，包括 `--yolo`；`--approval never` 或没有终端时拒绝。
- nil approver 使用 `DefaultApprover`，拒绝明确的 E/M/U。`UnattendedApprover` 同样拒绝，包括 `AllowAll`／定时任务的 `--allow-all`。本地写入的预批准豁免不能绕过明确的 E/M/U。
- E/M/U 未知或为 false 的请求继续沿用现有审批模式和 Risk 规则，包括无人确认时拒绝 `dangerous` / `confirm`。不会推导常驻授权。

P1 为 `gmail_send` 和 `calendar_create` 填写 E/U=true、M=false、R=false，为 `browser_navigate` 填写 E=true、R=false。`memory_add` 的 E/M/U=false、R=未知：它是持久本地写入，删除条目不能撤销后续已经发生的使用。其他工具可保留未知维度；本阶段不增加媒体端点的本地性／计费分类，也不增加 bash 后果分类。

`memory_add` 在两种 profile 中均实现 `ApprovalDescriber`。审批预览展示内容、scope 及请求的 workspace/global target（没有工作区上下文时均写入全局记忆）。`memory_search` 保持只读且不加审批。

`gmail_send` 接受可选 `as_draft`（coding 默认 `false`，personal 默认 `true`）。为 `true` 时创建真实 Gmail 草稿，返回**草稿 ID**，不发送；API 失败绝不回退到发送。预览和结果明确标注 “not sent”。草稿仍为 E/U=true，因为 Google 可见，且内容以用户身份创建在其邮箱内。草稿内容可以删除，但整个效果仍为 R=false，因为服务方痕迹无法在本地撤销。两种模式都必须人工确认，或匹配显式常驻授权。

创建草稿需要 `gmail.compose`，现已在原有 scope 之外加入新连接的授权请求。已有连接仍可发送；使用草稿前需通过 `gg connect google --force` 重新授权。缺少草稿 scope 会返回错误并提示重新连接。参见[连接器](connectors.zh-CN.md#安全边界)。

## 常驻授权边界（P2）

常驻授权（standing authorization）必须显式包含三部分：

| 部分 | 必须表达的含义 |
| --- | --- |
| **主体（Subject）** | 谁可以行动，以及代表哪个用户或账户。 |
| **范围（Scope）** | 允许涉及哪些资源、目的地和效果，以及收件人、花费或允许的权限变更等限制。宽泛目标本身不是授权范围。 |
| **有效期（TTL）** | 明确的有效时间窗口或到期时间。不能推导无限期授权，也不能复用已过期授权。 |

`USER.md` 中的偏好、记忆中的习惯、OAuth 连接或上一次批准，本身都不构成常驻授权。P2 除上述三部分外，还要求用户授权的**明文原话**。“你看着办”“随便”“you figure it out”“do what you think is best”“whatever”“as you like”均被拒绝，包括出现在更长引文中的情况。缺少结构化字段，或只有目标、没有工具／资源／效果限制，都会在存储前报错。原话仅供记录，绝不由模型解释成许可。

### 存储与 CLI

[`internal/standingauth`](../internal/standingauth/) 将每条授权保存到 `~/.gg/standing-auth/<id>.json`，与其他 gg 状态一样通过 `Config.HomeDir` 定位。目录权限为 0700；授权文件及 `audit.jsonl` 为 0600。文件原子发布，CLI 与运行中的 daemon 无需重启即可共享授权。记录包含：

| 字段 | 含义 |
| --- | --- |
| `id`、`createdAt` | 自动生成的稳定 ID 和 UTC 创建时间（有效期从此开始）。 |
| `subject` | 由用户显式提供的字符串，说明 gg 是行动者，以及代表哪个用户／账户。 |
| `scope.tools` | 非空、精确的工具名称白名单，不支持通配符。 |
| `scope.consequences` | 显式允许的 E/M/U 效果；`[]` 表示均不允许。R 本身不授予权限。 |
| `scope.arguments` | 非空 JSON 对象，精确限制顶层参数值，例如收件人与草稿模式。未列出的参数不受限制。 |
| `expiresAt` | 必须提供未来的 RFC3339 到期时间，由 `--ttl` 计算或通过 `--expires` 指定。不默认无限期。 |
| `sourceQuote`、`notes` | 必填的用户授权明文原话，以及可选备注。 |

```sh
gg standing-auth add \
  --subject 'gg on behalf of alice@example.com' \
  --tools gmail_send --consequences E,U \
  --arguments '{"to":"bob@example.com","as_draft":true}' \
  --ttl 24h \
  --source-quote '在 24 小时内，允许 gg 代表 alice@example.com 创建仅发给 bob@example.com 的 Gmail 草稿，不得发送。'
gg standing-auth list
gg standing-auth revoke <id>
```

新增后输出 ID。绝对到期时间可用 `--expires '2026-12-01T09:00:00+08:00'` 替代 `--ttl`；`--notes` 可选。明确无 E/M/U 效果时使用 `--consequences none`。列表显示主体、结构化范围、到期时间／状态及原话，也保留已过期授权。撤销可以重复执行；阻止后续匹配，但不能撤销已经批准的调用。

此例允许向精确收件人创建任意标题／正文的草稿，且**必须显式传入 `as_draft=true`**。缺少 `as_draft`、改为发送、换收件人或额外断言 M 后果，均不能匹配。可进一步精确限制内容参数。P2 不支持收件人模式、花费计数或自由文本范围解释。

### 共享匹配与审计

共享 turn executor 用 `standingauth.Approver` 包装调用方的审批器，覆盖交互 CLI、`--approval never`、`--yolo`、nil／默认审批器、手动／daemon 定时任务和 Telegram。只有走审批接口的工具参与；现有本地预批准豁免保持不变。未解析 `Config.HomeDir` 的嵌入方保留 P1 行为，不会从当前工作目录加载授权。

包装器在询问或拒绝之前，按 ID 顺序读取当前授权。匹配要求仍在有效期内、工具名精确命中、E/M/U 元数据全部已知、每个明确后果都在范围内、每个受限参数均存在且 JSON 值精确相等。未知后果不匹配；不同授权的范围不会合并。`dangerous`、`confirm` 和未知 Risk 类别**始终不匹配**，即使存在 E/M/U 授权。P2 不提供豁免这些类别的开关，继续交由现有人工审批／拒绝路径处理。

命中后先追加并同步审计记录，包含 `timestamp`、`grantId`、`toolName`、`summary` 和 `decision="allow-by-standing-grant"`，再无提示放行。存储损坏／读取失败或审计写入失败时，均失败关闭。审计记录的是授权决定，不代表执行成功。未命中时，原有 P1 审批器保持不变。

授权适用于使用同一 gg home 的所有 profile、工作区和传输入口。`subject` 记录显式声明的行动者／账户；选择 profile 不会限定授权范围或验证 OAuth 账户。实际配置账户应与声明一致，切换时应撤销并重新授权。Telegram 的聊天白名单仍控制谁可以发起轮次；Telegram 保持 `UnattendedApprover{}`，不会变为 AllowAll。

定时任务的 `--allow-all` 为兼容保留：仍按 P1 规则放行普通工具，但不能豁免明确的 E/M/U 或 dangerous/confirm 风险。在这些走审批接口的调用中，匹配常驻授权是无人值守时允许明确 E/M/U 的唯一授权路径；命中不需要 `--allow-all`。

### TODO／后续工作

- 手动任务（`internal/cliapp/job.go`）和 daemon 任务（`internal/daemon/scheduler.go`）仍传入 `AllowAll: job.AutoApprove`。替换普通工具的兼容路径需要明确迁移方案；P2 不做整体删除。
- 未分类调用还需补齐后果元数据，授权才能匹配，包括 bash／MCP／媒体工具及浏览器导航中未知的 M/U。未实现 `ApprovalDescriber` 的工具仍在此拦截范围之外。
- 账户／行动者验证、按传输入口划分授权主体、花费／使用量限制及更丰富的目的地限制留待后续。

## 当前审批清单

P0（#54）之后的 P1 清单，对照了[内置工具实现](../internal/tools/)、[工具注册表](../internal/app/toolregistry.go)和 [MCP 桥接](../internal/mcp/tool.go)。覆盖 **30 个内置工具：16 个实现、14 个未实现 `agent.ApprovalDescriber`**，另加全部动态注册的 MCP 工具。实际注册取决于配置、已授予的 scope、provider 可用性和平台支持，并非每个会话都会暴露全部工具。

[`agent.ApprovalDescriber`](../internal/agent/types.go) 是包含 `ApprovalRequest` 方法的接口。“是”表示工具向 [runner](../internal/agent/runner.go)提供审批请求，不表示每次调用都会弹出人工确认。现有策略可以允许或拒绝请求，`write`/`edit` 还有狭窄的预批准豁免。“否”表示该接口不拦截此工具，不表示工具没有后果。

表中的 R/E/M/U 是上述维度的缩写，描述的是效果，并不表示每个工具都已填写运行时元数据。R 描述整个效果是否可逆；“视情况”需要具体调用的证据。E/M/U 表示典型或条件性直接后果，不是完整策略。同步目的地、应用行为和远程端点都可能改变判断。

### 已实现 ApprovalDescriber 的工具

| 工具 | ApprovalDescriber？ | 典型后果维度 | 说明／个人模式缺口？ |
| --- | --- | --- | --- |
| [`write`](../internal/tools/write.go) | 是 | R：取决于能否恢复被覆盖内容；通常是本地操作。 | 创建／覆盖工作区文件。真实 `.gg/agent/` 目录中的写入预先放行；本阶段保留此豁免。 |
| [`edit`](../internal/tools/edit.go) | 是 | R：取决于能否恢复旧内容；通常是本地操作。 | 精确替换并展示修改前后预览。具有同样的真实 `.gg/agent/` 目录预批准豁免。 |
| [`bash`](../internal/tools/bash.go) | 是 | R/E/M/U：取决于命令和目标。 | 现有分类器和可选风险检查填写 `risk`/`riskReason`。shell 命令可能涉及任意维度。 |
| [`memory_add`](../internal/tools/memory_add.go) | 是 | R：未知，取决于能否在复用前移除条目；E/M/U：false。 | 持久写入工作区／全局记忆，现已在共享运行时加审批。预览展示内容、scope 和 target；可影响后续轮次。 |
| [`browser_navigate`](../internal/tools/browser.go) | 是 | R：第三方请求痕迹不可逆；E。 | 初始 URL 经 SSRF 检查，导航走审批。重定向和子资源限制仍见架构文档。 |
| [`gmail_send`](../internal/tools/connectors.go) | 是 | R：外部痕迹不可逆；E、U。 | Coding 默认发送，personal 默认草稿；显式 `as_draft` 覆盖两者默认值。两种模式均断言 E/U；删除草稿内容不能撤销服务方痕迹。 |
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
| [`notify`](../internal/tools/computer.go) | 否 | R：已展示／打扰后不可逆；E（OS 通知）。 | **潜在的个人模式缺口。** 当前视作无害且不走审批；内容在 OS 界面上可见，也可能打扰用户。此缺口仍在 P1 最小覆盖范围之外。 |
| [`subagent`](../internal/tools/subagent.go) | 否 | R：取决于 provider 痕迹；远程聊天 provider 涉及 E。 | 子工具仅有 `read`/`list`/`grep`。会增加普通聊天调用，仅普通聊天费用不算 M。子工具只读不代表处理全在本地完成。 |
| [`kb_search`](../internal/tools/kb_search.go) | 否 | R：视情况；远程 embedding 端点涉及 E，计费时涉及 M。 | 本地索引只读，但会经配置的 API 为查询生成 embedding。披露／花费是另一个个人模式待评估点；“搜索”不保证没有外部后果。 |

## 现有策略与阶段边界

当前运行时策略见架构文档的[安全模型](architecture.zh-CN.md#安全模型)和[审批模式](architecture.zh-CN.md#审批模式)。P1 将 #53 对 `dangerous`/`confirm` 风险的 nil approver 失败关闭处理扩展到明确的 E/M/U 后果，同时保留未分类工具现有的 Risk 行为。

P1 已交付上述字段、路由、记忆写入审批和可选 Gmail 草稿开关。P2 已交付显式常驻授权的持久存储、CLI 管理、共享匹配与放行审计。P3 已交付 personal profile 选择器、个人基础身份、更严默认与 Gmail 草稿偏好，并继续共享运行时。P4 首个切片交付上方本地主动性策略及假数据自用 CLI；更广泛的投递仍按已述停止条件延后。
