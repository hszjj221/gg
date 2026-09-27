package cliapp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
)

const connectUsage = `usage:
  gg connect google [--client-id ID] [--client-secret SECRET] [--manual] [--force]
  gg connect list
  gg connect status [google]
  gg connect remove google`

const googleClientSetupHelp = `
需要一个 Google OAuth 客户端（"Desktop app" 类型）：
  1. 打开 https://console.cloud.google.com/apis/credentials
  2. 创建凭据 → OAuth 客户端 ID → 应用类型选"桌面设备"
  3. 把 Client ID 传进来：
       gg connect google --client-id YOUR_CLIENT_ID
     也可以写进配置文件 (~/.gg/config.json):
       {"connectors": {"google": {"clientId": "...", "clientSecret": "..."}}}
     或环境变量 GG_GOOGLE_CLIENT_ID / GG_GOOGLE_CLIENT_SECRET
注意：gmail.send 属于敏感 scope，个人使用时 Google Cloud 项目保持"测试模式"即可。`

// runConnectCommand implements `gg connect ...`: manage third-party
// service connections (OAuth).
func runConnectCommand(ctx context.Context, cfg config.Config, connectArgs []string, stdout, stderr io.Writer) int {
	if len(connectArgs) == 0 {
		fmt.Fprintln(stdout, connectUsage)
		return 2
	}
	store, err := connector.Open(cfg.Connectors.Dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch connectArgs[0] {
	case "google":
		return runConnectGoogle(ctx, cfg, store, connectArgs[1:], stdout, stderr)
	case "list":
		return runConnectList(store, stdout, stderr)
	case "status":
		return runConnectStatus(store, connectArgs[1:], stdout, stderr)
	case "remove":
		return runConnectRemove(store, connectArgs[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, connectUsage)
		return 2
	}
}

func runConnectGoogle(ctx context.Context, cfg config.Config, store *connector.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg connect google", flag.ContinueOnError)
	fs.SetOutput(stderr)
	clientID := fs.String("client-id", "", "Google OAuth client ID (Desktop app type)")
	clientSecret := fs.String("client-secret", "", "Google OAuth client secret (optional for Desktop apps)")
	manual := fs.Bool("manual", false, "headless: paste the full redirect URL instead of localhost callback")
	force := fs.Bool("force", false, "re-authorize even when already connected")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	id := first(*clientID, cfg.Connectors.Google.ClientID)
	secret := first(*clientSecret, cfg.Connectors.Google.ClientSecret)
	if id == "" {
		fmt.Fprintln(stderr, "缺少 Google OAuth client ID。"+googleClientSetupHelp)
		return 2
	}

	if !*force {
		if tok, err := store.Load(google.Name); err == nil {
			fmt.Fprintf(stdout, "google 已连接（scopes: %s，access token 有效期至 %s）。\n",
				strings.Join(tok.Scopes, " "), tok.Expiry.Format("2006-01-02 15:04"))
			fmt.Fprintln(stdout, "重新授权请加 --force。")
			return 0
		}
	}

	flowCfg := google.Config{ClientID: id, ClientSecret: secret}.FlowConfig(*manual)
	fmt.Fprintln(stdout, "正在启动 Google 授权（Gmail 读取/发送 + 日历事件）…")
	tok, err := connector.RunFlow(ctx, flowCfg)
	if err != nil {
		fmt.Fprintln(stderr, "授权失败：", err)
		return 1
	}
	if len(tok.Scopes) == 0 {
		tok.Scopes = google.Scopes
	}
	if err := store.Save(google.Name, tok); err != nil {
		fmt.Fprintln(stderr, "保存 token 失败：", err)
		return 1
	}
	fmt.Fprintln(stdout, "google 连接成功！agent 现在可以使用 gmail_search / gmail_read / gmail_send / calendar_agenda / calendar_create。")
	return 0
}

func runConnectList(store *connector.Store, stdout, stderr io.Writer) int {
	names, err := store.List()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "还没有连接。运行 `gg connect google` 开始。")
		return 0
	}
	for _, n := range names {
		fmt.Fprintln(stdout, n)
	}
	return 0
}

func runConnectStatus(store *connector.Store, args []string, stdout, stderr io.Writer) int {
	name := google.Name
	if len(args) > 0 {
		name = args[0]
	}
	if name != google.Name {
		fmt.Fprintf(stderr, "未知的 connector %q（目前只支持 google）\n", name)
		return 2
	}
	tok, err := store.Load(name)
	if err != nil {
		if errors.Is(err, connector.ErrNotConnected) {
			fmt.Fprintf(stdout, "%s 未连接。运行 `gg connect %s` 开始。\n", name, name)
			return 1
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	status := "有效"
	if tok.Expired() {
		status = "已过期（下次使用时自动刷新）"
	}
	fmt.Fprintf(stdout, "connector: %s\nscopes: %s\naccess token: %s，有效期至 %s\nrefresh token: 已保存\n",
		name, strings.Join(tok.Scopes, " "), status, tok.Expiry.Format(time.RFC3339))
	return 0
}

func runConnectRemove(store *connector.Store, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: gg connect remove google")
		return 2
	}
	if args[0] != google.Name {
		fmt.Fprintf(stderr, "未知的 connector %q（目前只支持 google）\n", args[0])
		return 2
	}
	if err := store.Remove(args[0]); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "已断开 %s。\n", args[0])
	return 0
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
