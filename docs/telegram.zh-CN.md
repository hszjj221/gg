# Telegram 频道

daemon（`ggd`）可以通过 long polling 把 agent 变成 Telegram bot。不需要 webhook，也不需公网部署。

## 配置

1. 用 [@BotFather](https://t.me/BotFather) 创建 bot，复制 token。
2. 查你的 chat ID（比如用 [@userinfobot](https://t.me/userinfobot)）。
3. 带上环境变量启动 daemon：

```bash
export GG_TELEGRAM_BOT_TOKEN="<bot-token>"
export GG_TELEGRAM_ALLOW_CHATS="<your-chat-id>"
ggd
```

只有 `GG_TELEGRAM_ALLOW_CHATS`（逗号分隔的整数）里的 chat 会被应答。空 allowlist 意味着 bot 谁也不理——默认绝不跟陌生人说话。

token 只从环境变量读取，绝不打日志；包含它的 API URL 在 `internal/telegram` 内部拼装，绝不打印。

## 行为

- 每个 chat 有自己的 agent 会话（`telegram:<chat_id>`），和其他会话一样持久化——daemon 重启后上下文还在。
- 每个 chat 同时只跑一轮；不同 chat 并发。
- 文字消息走 agent；最终文本发回（按 4096 字符分片）。
- 语音消息走语音流水线：`getFile` → 下载到 `~/.gg/media/telegram/chat-<id>/`（用完即删）→ STT → agent → 文字回复 → TTS（`opus`）→ 语音回复。文字回复永远先发，这样 TTS 失败也不会丢答案。
- 需要 approval 的工具会被拒绝：bot 没法交互式提问，所以 approval-gated 的工具在 Telegram 上不可用。

## 可靠性

- `getUpdates` long-poll 窗口 30s；每批处理完 offset 持久化到 `~/.gg/telegram/offset`，重启不重复处理。
- 内存里的短窗口去重会丢弃重复的 `update_id`。
- 429 按 `retry_after` 等待；其他错误指数退避（1s → 最大 30s）。
- 语音下载上限 25 MiB。

## 验收测试

给 bot 发一条语音，问今天的日程。预期：bot 先转写，agent 通过 Google connector 读日历，你同时收到文字回复和语音回复。
