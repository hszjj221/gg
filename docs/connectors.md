# Connectors（外部服务连接）

gg 通过 connector 把外部服务接给 agent。目前支持 **Google**（Gmail + Google Calendar），后续按同一框架扩展。

## 连接 Google

```bash
gg connect google --client-id YOUR_CLIENT_ID
```

流程：

1. gg 在 `127.0.0.1` 起一个临时回调服务，打印授权 URL（并尝试自动打开浏览器）。
2. 在浏览器里用你的 Google 账号同意授权（Gmail 读取/发送、日历事件）。
3. 回调成功后，token 存到 `~/.gg/connectors/google.json`（0600，只有你能读）。

无浏览器/远程机器时加 `--manual`：授权后浏览器会显示无法连接，把地址栏的完整 URL 粘贴回终端即可：

```bash
gg connect google --client-id YOUR_CLIENT_ID --manual
```

### Client ID 从哪来

gg 是开源软件，不能内置 Google OAuth 凭据。你需要自己创建一个（一次）：

1. 打开 https://console.cloud.google.com/apis/credentials
2. 创建凭据 → OAuth 客户端 ID → 应用类型选 **桌面设备**（Desktop app）
3. 启用 **Gmail API** 和 **Google Calendar API**（API 库里搜索启用）
4. 把 Client ID 传给 `gg connect google`

Desktop app 类型不需要 client secret（走 PKCE）。个人使用时，Google Cloud 项目保持"测试模式"即可（`gmail.send` 是敏感 scope，测试模式下自用足够；要公开发布再走 Google 验证）。

Client ID 也可以写进 `~/.gg/config.json` 或环境变量，避免每次传 flag：

```json
{"connectors": {"google": {"clientId": "...", "clientSecret": "..."}}}
```

```bash
export GG_GOOGLE_CLIENT_ID=...
export GG_GOOGLE_CLIENT_SECRET=...   # 可选
```

优先级：`--client-id` flag > 环境变量 > 配置文件。

### 管理连接

```bash
gg connect list            # 已连接的服务
gg connect status google   # scopes、token 有效期
gg connect remove google   # 断开（删除本地 token）
```

## Agent 能做什么

连接成功后，agent 自动获得 5 个工具（未连接时不存在）：

| 工具 | 说明 | 审批 |
|---|---|---|
| `gmail_search` | 按 Gmail 搜索语法查邮件，如 `newer_than:1d`、`from:boss@example.com`、`is:unread` | 否 |
| `gmail_read` | 读一封邮件（From/Subject/Date + 正文） | 否 |
| `gmail_send` | 发邮件 | **是** |
| `calendar_agenda` | 查未来 N 天日程（默认今天） | 否 |
| `calendar_create` | 创建日历事件 | **是** |

示例：

- "总结一下今天的邮件" → `gmail_search("newer_than:1d")` → 逐封 `gmail_read` → 中文总结
- "我明天下午有什么会" → `calendar_agenda`（days=2）
- "帮我约个明天上午 10 点的会" → `calendar_create`（需你确认）

申请的 scope 是最小集：`gmail.readonly` + `gmail.send`（不要 `gmail.modify` 全权）+ `calendar.events`（只要事件读写，不要整个日历设置）。

## 安全边界

- OAuth 回调只绑 `127.0.0.1`，随机端口，`state` 防 CSRF。
- token 文件 0600、目录 0700；access token 过期自动用 refresh token 续（跨进程加锁，不会重复刷新）。
- `gmail_send` / `calendar_create` 必须经过你的确认（preview 显示收件人/主题/时间）；定时任务默认会被拒绝，除非任务显式开了 `--allow-all`。
- 日志和报错里永远不会打印 token。
- 撤销：在 Google 账号的"第三方访问权限"里移除，或本地 `gg connect remove google`。
