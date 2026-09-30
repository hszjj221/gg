# Browser (docs/browser.md)

gg drives a local headless Chromium over **Chrome DevTools Protocol (CDP)**.
The implementation is a minimal, stdlib-only WebSocket client (`internal/browser/ws`) with no third-party dependencies.

## Capabilities (phase one: read + screenshot)

- `browser_navigate {url}`: loads the page, waits for load completion, returns the title.
- `browser_read`: returns the rendered text of the current page (`document.body.innerText`, truncated at 12,000 runes).
- `browser_screenshot`: captures a viewport PNG into `~/.gg/media/screenshots/` (0600).

Form filling, clicking, and other write operations come in a later phase and will require approval.

## Requirements

A local Chromium is required: probed in order `chromium`, `chromium-browser`, `google-chrome`, `google-chrome-stable` (on macOS, Chrome/Chromium under `/Applications`), or specify the binary path directly with `GG_CHROMIUM`. When none is found the tools return an explicit error instead of failing silently.

Chromium starts with `--headless=new --remote-debugging-port=0` (sandbox kept by default; disabled only with `GG_BROWSER_NO_SANDBOX=1`, for environments like containers whose kernels don't support sandboxing). One browser session is shared per session (Service), started lazily, and sessions never interfere with each other; the process and temporary profile are cleaned up when the session closes.

## Security boundaries

- `browser_navigate` and the CLI only accept `http(s)` URLs; `file:`, `javascript:` and other schemes are rejected.
- Screenshots/PNGs are only written to `~/.gg/media/screenshots/`, 0600.

## CLI

```bash
gg browser shot https://example.com   # screenshot, prints the PNG path
gg browser read https://example.com   # prints the page's rendered text
```
