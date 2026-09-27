# Telegram channel

The daemon (`ggd`) can expose the agent as a Telegram bot via long polling.
No webhook or public deployment is needed.

## Setup

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy the token.
2. Find your chat ID (e.g. via [@userinfobot](https://t.me/userinfobot)).
3. Start the daemon with:

```bash
export GG_TELEGRAM_BOT_TOKEN="<bot-token>"
export GG_TELEGRAM_ALLOW_CHATS="<your-chat-id>"
ggd
```

Only chats in `GG_TELEGRAM_ALLOW_CHATS` (comma-separated integers) are
answered. An empty allowlist means the bot answers nobody — it never talks
to strangers by default.

The token is read from the environment only. It is never logged; API URLs
containing it are built inside `internal/telegram` and never printed.

## Behavior

- Each chat gets its own agent session (`telegram:<chat_id>`), persisted
  like any other session — conversation context survives daemon restarts.
- One in-flight turn per chat; different chats run concurrently.
- Text messages run the agent; the final text is sent back (split into
  4096-char chunks).
- Voice/audio messages run the voice pipeline:
  `getFile` → download to `~/.gg/media/telegram/chat-<id>/` (deleted after
  use) → STT → agent → text reply → TTS (`opus`) → voice reply.
  The text reply is always sent first so a TTS failure never loses the answer.
- Tools that require approval are denied: the bot cannot ask interactively,
  so approval-gated tools are unavailable over Telegram.

## Reliability

- `getUpdates` long-polls with a 30s window; the offset is persisted to
  `~/.gg/telegram/offset` after every batch, so restarts don't reprocess.
- A short in-memory replay guard drops duplicate `update_id`s.
- 429 responses honor `retry_after`; other errors back off exponentially
  (1s → 30s max).
- Voice downloads are capped at 25 MiB.

## Acceptance test

Send the bot a voice message asking about today's calendar. Expected:
the bot transcribes it, the agent reads the calendar via the Google
connector, and you get both a text reply and a voice reply.
