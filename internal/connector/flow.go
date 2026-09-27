package connector

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// FlowConfig drives one OAuth 2.0 authorization-code flow.
type FlowConfig struct {
	AuthURL      string // authorization endpoint
	TokenURL     string // token endpoint
	ClientID     string
	ClientSecret string // may be empty for PKCE-only installed apps

	Scopes []string

	// Manual, when true, prints the URL and reads the ?code= value from
	// stdin instead of starting a localhost callback server (headless).
	Manual bool

	// Timeout bounds the whole flow (browser wait included).
	Timeout time.Duration
	// HTTPClient is used for the token exchange; nil means http.DefaultClient.
	HTTPClient *http.Client
	// OpenBrowser, when non-nil, is called with the auth URL. The default
	// tries the platform opener and ignores failure.
	OpenBrowser func(url string)
	// ReadCode, when non-nil, supplies the pasted code in Manual mode.
	ReadCode func() (string, error)
	// ListenAddr overrides the localhost bind (tests); "" means 127.0.0.1:0.
	ListenAddr string
}

// TokenResponse is the token endpoint's JSON body.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// RunFlow runs the authorization-code flow and returns the token.
// It binds the callback server to 127.0.0.1 only.
func RunFlow(ctx context.Context, cfg FlowConfig) (Token, error) {
	if cfg.ClientID == "" {
		return Token{}, fmt.Errorf("client id is required")
	}
	if cfg.AuthURL == "" || cfg.TokenURL == "" {
		return Token{}, fmt.Errorf("auth and token endpoints are required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	state, err := randomString(16)
	if err != nil {
		return Token{}, err
	}
	verifier, err := randomString(64)
	if err != nil {
		return Token{}, err
	}
	challenge := pkceChallenge(verifier)

	if cfg.Manual {
		return runManualFlow(ctx, cfg, state, verifier, challenge)
	}
	return runLocalhostFlow(ctx, cfg, state, verifier, challenge)
}

func runLocalhostFlow(ctx context.Context, cfg FlowConfig, state, verifier, challenge string) (Token, error) {
	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return Token{}, fmt.Errorf("listen on localhost: %w", err)
	}
	defer ln.Close()

	redirectURI := "http://" + ln.Addr().String() + "/callback"
	authURL := buildAuthURL(cfg, state, challenge, redirectURI)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != state {
			errCh <- fmt.Errorf("state mismatch (possible CSRF)")
			fmt.Fprint(w, callbackPage("授权失败：state 不匹配，请重试。"))
			return
		}
		if ec := q.Get("error"); ec != "" {
			errCh <- fmt.Errorf("authorization failed: %s", ec)
			fmt.Fprintf(w, callbackPage("授权失败：%s，请重试。"), ec)
			return
		}
		code := q.Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			fmt.Fprint(w, callbackPage("授权失败：没有收到授权码，请重试。"))
			return
		}
		fmt.Fprint(w, callbackPage("授权成功！可以关闭这个页面，回到终端继续。"))
		codeCh <- code
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	openAuthURL(cfg, authURL)
	fmt.Printf("\n请在浏览器中打开以下地址完成授权：\n\n  %s\n\n等待授权…\n", authURL)

	select {
	case code := <-codeCh:
		return exchange(ctx, cfg, code, verifier, redirectURI)
	case err := <-errCh:
		return Token{}, err
	case <-ctx.Done():
		return Token{}, fmt.Errorf("authorization timed out")
	}
}

func runManualFlow(ctx context.Context, cfg FlowConfig, state, verifier, challenge string) (Token, error) {
	// Headless machines have no localhost browser path: Google retired the
	// OOB ("urn:ietf:wg:oauth:2.0:oob") flow, so we use a dummy localhost
	// redirect URI and have the user paste the full address-bar URL after
	// Google redirects (the browser shows a connection error, but the URL
	// carries ?code=...&state=...).
	const redirectURI = "http://127.0.0.1:9/callback"
	authURL := buildAuthURL(cfg, state, challenge, redirectURI)
	openAuthURL(cfg, authURL)
	fmt.Printf("\n请在浏览器中打开以下地址完成授权：\n\n  %s\n\n", authURL)
	fmt.Print("浏览器会显示无法连接，把地址栏的完整 URL 粘贴到这里：")
	read := cfg.ReadCode
	if read == nil {
		read = readLine
	}
	pasted, err := read()
	if err != nil {
		return Token{}, err
	}
	pasted = strings.TrimSpace(pasted)
	code, err := parseManualRedirect(pasted, state)
	if err != nil {
		return Token{}, err
	}
	return exchange(ctx, cfg, code, verifier, redirectURI)
}

// parseManualRedirect extracts and validates the code from a pasted
// redirect URL (manual/headless flow).
func parseManualRedirect(pasted, state string) (string, error) {
	u, err := url.Parse(pasted)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Query().Get("state") != state {
		return "", fmt.Errorf("state mismatch (possible CSRF)")
	}
	if ec := u.Query().Get("error"); ec != "" {
		return "", fmt.Errorf("authorization failed: %s", ec)
	}
	code := u.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("no code in the pasted URL")
	}
	return code, nil
}

func buildAuthURL(cfg FlowConfig, state, challenge, redirectURI string) string {
	q := url.Values{}
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("access_type", "offline") // ask for a refresh token
	q.Set("prompt", "consent")      // force re-consent so refresh_token is issued
	return cfg.AuthURL + "?" + q.Encode()
}

// exchange trades the authorization code for tokens (PKCE).
func exchange(ctx context.Context, cfg FlowConfig, code, verifier, redirectURI string) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", verifier)
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, fmt.Errorf("read token response: %w", err)
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Token{}, fmt.Errorf("decode token response: %w", err)
	}
	if tr.Error != "" {
		return Token{}, fmt.Errorf("token exchange failed: %s %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return Token{}, fmt.Errorf("token exchange returned no access token")
	}
	expiry := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if tr.ExpiresIn <= 0 {
		expiry = time.Now().Add(time.Hour)
	}
	return Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expiry:       expiry,
		Scopes:       strings.Fields(tr.Scope),
	}, nil
}

// RefreshToken exchanges a refresh token for a new access token.
func RefreshToken(ctx context.Context, cfg FlowConfig, refreshToken string) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", cfg.ClientID)
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("token refresh: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, fmt.Errorf("read refresh response: %w", err)
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Token{}, fmt.Errorf("decode refresh response: %w", err)
	}
	if tr.Error != "" {
		return Token{}, fmt.Errorf("token refresh failed: %s %s (reconnect with `gg connect`)", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return Token{}, fmt.Errorf("token refresh returned no access token")
	}
	// Providers may omit a new refresh token; keep the old one.
	if tr.RefreshToken == "" {
		tr.RefreshToken = refreshToken
	}
	expiry := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if tr.ExpiresIn <= 0 {
		expiry = time.Now().Add(time.Hour)
	}
	return Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expiry:       expiry,
		Scopes:       strings.Fields(tr.Scope),
	}, nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openAuthURL(cfg FlowConfig, authURL string) {
	if cfg.OpenBrowser != nil {
		cfg.OpenBrowser(authURL)
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", authURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", authURL)
	default:
		cmd = exec.Command("xdg-open", authURL)
	}
	_ = cmd.Start() // best-effort; the URL is always printed
}

func callbackPage(msg string) string {
	return "<!doctype html><html><head><meta charset=utf-8><title>gg</title></head>" +
		"<body style=\"font-family:sans-serif;text-align:center;padding-top:20vh\">" +
		"<h2>" + msg + "</h2></body></html>"
}

func readLine() (string, error) {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
