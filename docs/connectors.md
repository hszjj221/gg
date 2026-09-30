# Connectors (third-party service connections)

gg connects external services to the agent through connectors. **Google** (Gmail + Google Calendar) is supported today; more will follow the same framework.

## Connect Google

```bash
gg connect google --client-id YOUR_CLIENT_ID
```

The flow:

1. gg starts a temporary callback server on `127.0.0.1`, prints the authorization URL (and tries to open the browser automatically).
2. Consent in the browser with your Google account (Gmail read/send, calendar events).
3. After a successful callback, the token is stored in `~/.gg/connectors/google.json` (0600, readable only by you).

On headless/remote machines add `--manual`: after authorizing, the browser shows a connection failure; paste the full URL from the address bar back into the terminal:

```bash
gg connect google --client-id YOUR_CLIENT_ID --manual
```

### Where to get a Client ID

gg is open-source and cannot ship Google OAuth credentials. Create your own (once):

1. Open https://console.cloud.google.com/apis/credentials
2. Create credentials → OAuth client ID → application type **Desktop app**
3. Enable the **Gmail API** and **Google Calendar API** (search and enable in the API library)
4. Pass the Client ID to `gg connect google`

The Desktop app type needs no client secret (PKCE flow). For personal use, keeping the Google Cloud project in "Testing" mode is enough (`gmail.send` is a sensitive scope; testing mode suffices for self-use — go through Google verification only if you publish publicly).

> Note: in testing mode, refresh tokens **expire after 7 days** (a Google rule; sensitive scopes like Gmail/Calendar are not exempt). Re-run `gg connect google` after 7 days. For long-term use, set the OAuth consent screen to "Published" (requires Google verification; or keep yourself in the test-user list — publishing lifts the test-user restriction and tokens no longer expire in 7 days).

The Client ID can also go into `~/.gg/config.json` or environment variables so you don't pass the flag every time:

```json
{"connectors": {"google": {"clientId": "...", "clientSecret": "..."}}}
```

```bash
export GG_GOOGLE_CLIENT_ID=...
export GG_GOOGLE_CLIENT_SECRET=...   # optional
```

Precedence: `--client-id` flag > environment variables > config file.

### Manage connections

```bash
gg connect list            # connected services
gg connect status google   # scopes, token expiry
gg connect remove google   # disconnect (deletes the local token)
```

## What the agent can do

Once connected, the agent automatically gets 5 tools (they don't exist when disconnected):

| Tool | Description | Approval |
|---|---|---|
| `gmail_search` | Search mail with Gmail syntax, e.g. `newer_than:1d`, `from:boss@example.com`, `is:unread` | No |
| `gmail_read` | Read one message (From/Subject/Date + body) | No |
| `gmail_send` | Send mail | **Yes** |
| `calendar_agenda` | List events for the next N days (today by default) | No |
| `calendar_create` | Create a calendar event | **Yes** |

Examples:

- "Summarize today's mail" → `gmail_search("newer_than:1d")` → `gmail_read` each → Chinese summary
- "What meetings do I have tomorrow afternoon" → `calendar_agenda` (days=2)
- "Schedule a meeting for 10 AM tomorrow" → `calendar_create` (needs your confirmation)

The requested scopes are the minimal set: `gmail.readonly` + `gmail.send` (no full `gmail.modify`) + `calendar.events` (event read/write only, not whole-calendar settings).

## Security boundaries

- The OAuth callback binds only to `127.0.0.1`, on a random port, with `state` CSRF protection.
- Token file 0600, directory 0700; expired access tokens are refreshed automatically via the refresh token (cross-process locking, no duplicate refreshes).
- `gmail_send` / `calendar_create` require your confirmation (preview shows recipients/subject/time); scheduled jobs are denied by default unless the job explicitly sets `--allow-all`.
- Tokens are never printed in logs or error messages.
- To revoke: remove access under "Third-party access" in your Google account, or run `gg connect remove google` locally.
