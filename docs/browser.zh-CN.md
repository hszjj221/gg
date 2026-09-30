# 浏览器（docs/browser.md）

gg 通过 **Chrome DevTools Protocol (CDP)** 驱动本地 headless Chromium。
实现是纯标准库的最小 WS 客户端（`internal/browser/ws`），无第三方依赖。

## 能力（第一期：读 + 截图）

- `browser_navigate {url}`：加载页面并等待 load 完成，返回标题。
- `browser_read`：返回当前页渲染文本（`document.body.innerText`，截断 12000 rune）。
- `browser_screenshot`：截取视口 PNG，存到 `~/.gg/media/screenshots/`（0600）。

填表/点击等写操作是下一期，且必须走 approval。

## 运行要求

需要本机有 Chromium：按 `chromium`、`chromium-browser`、`google-chrome`、
`google-chrome-stable` 顺序查找（macOS 看 `/Applications` 下的 Chrome/Chromium），
或用 `GG_CHROMIUM` 直接指定二进制路径。找不到时工具返回明确错误，不静默失败。

Chromium 以 `--headless=new --remote-debugging-port=0` 启动（默认保留
sandbox；仅当 `GG_BROWSER_NO_SANDBOX=1` 时禁用，用于容器等内核不支持
sandbox 的环境）。每个会话（Service）复用一个 session（懒启动），
会话间互不干扰；进程与临时 profile 随 session 关闭而清理。

## 安全边界

- `browser_navigate` 与 CLI 只接受 `http(s)` URL，拒绝 `file:`、`javascript:` 等 scheme。
- 截图/PNG 只写 `~/.gg/media/screenshots/`，0600。

## CLI

```bash
gg browser shot https://example.com   # 截图，输出 PNG 路径
gg browser read https://example.com   # 输出页面渲染文本
```
