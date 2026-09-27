// Package google implements the gg connector for Google services
// (Gmail + Google Calendar) on top of the connector framework.
// It uses plain net/http against the Google REST APIs; no SDK.
package google

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/filelock"
)

// Connector name used in the token store (~/.gg/connectors/google.json).
const Name = "google"

const (
	ScopeGmailRead = "https://www.googleapis.com/auth/gmail.readonly"
	ScopeGmailSend = "https://www.googleapis.com/auth/gmail.send"
	ScopeCalendar  = "https://www.googleapis.com/auth/calendar.events"
)

// Scopes is the minimal set covering Gmail read/send and Calendar events.
var Scopes = []string{ScopeGmailRead, ScopeGmailSend, ScopeCalendar}

const (
	AuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL = "https://oauth2.googleapis.com/token"
)

// Endpoints can be overridden in tests.
var (
	APIBase = "https://gmail.googleapis.com"
	CalBase = "https://www.googleapis.com"
)

// Config carries the OAuth client credentials for the flow and refresh.
type Config struct {
	ClientID     string
	ClientSecret string
	TokenURL     string // override for tests; "" means the Google default
	HTTPClient   *http.Client
}

// FlowConfig builds the connector.FlowConfig for `gg connect google`.
func (c Config) FlowConfig(manual bool) connector.FlowConfig {
	tokenURL := c.TokenURL
	if tokenURL == "" {
		tokenURL = TokenURL
	}
	return connector.FlowConfig{
		AuthURL:      AuthURL,
		TokenURL:     tokenURL,
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Scopes:       Scopes,
		Manual:       manual,
		HTTPClient:   c.HTTPClient,
	}
}

// Client is an authenticated Google API client. It refreshes the access
// token on demand (serialized across processes) and retries once on 401.
type Client struct {
	store   *connector.Store
	flowCfg connector.FlowConfig
	http    *http.Client
	mu      sync.Mutex
	token   connector.Token
	loaded  bool
}

// NewClient loads the stored token; use Connect first when there is none.
func NewClient(store *connector.Store, cfg Config) (*Client, error) {
	tokenURL := cfg.TokenURL
	if tokenURL == "" {
		tokenURL = TokenURL
	}
	c := &Client{
		store: store,
		flowCfg: connector.FlowConfig{
			TokenURL:     tokenURL,
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			HTTPClient:   cfg.HTTPClient,
		},
	}
	// The credentials given at connect time are persisted with the token:
	// a later process (agent run, daemon) must be able to refresh even when
	// no client id is configured via flag/env/file.
	if c.flowCfg.ClientID == "" {
		if tok, err := store.Load(Name); err == nil {
			c.flowCfg.ClientID = tok.ClientID
			c.flowCfg.ClientSecret = tok.ClientSecret
		}
	} else if c.flowCfg.ClientSecret == "" {
		// Same client ID from config but no secret: pick up the secret
		// saved at connect time, so refresh works for clients that
		// require one.
		if tok, err := store.Load(Name); err == nil && tok.ClientID == c.flowCfg.ClientID {
			c.flowCfg.ClientSecret = tok.ClientSecret
		}
	}
	c.http = &http.Client{Transport: &authTransport{client: c}}
	if cfg.HTTPClient != nil {
		c.http.Transport = &authTransport{client: c, base: cfg.HTTPClient.Transport}
	}
	return c, nil
}

func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		tok, err := c.store.Load(Name)
		if err != nil {
			return err
		}
		c.token = tok
		c.loaded = true
	}
	if c.token.Expired() {
		if err := c.refreshLocked(ctx, false); err != nil {
			return err
		}
	}
	return nil
}

// refreshLocked refreshes under a cross-process lock so concurrent
// processes do not double-refresh. The in-process mutex is held too.
// With force=true the token endpoint is always hit (401 path): the stored
// token may look unexpired yet already be rejected by Google.
// It never resurrects a removed connection: if the token file was deleted
// (gg connect remove) or replaced (re-authorize --force) while the refresh
// was in flight, the save is skipped.
func (c *Client) refreshLocked(ctx context.Context, force bool) error {
	unlock, err := filelock.Lock(c.store.Dir() + "/google-refresh.lock")
	if err != nil {
		return err
	}
	defer unlock()
	stored, err := c.store.Load(Name)
	if err != nil {
		return err
	}
	if stored.RefreshToken == "" {
		return fmt.Errorf("connector %s has no refresh token; run gg connect %s again", Name, Name)
	}
	// Another process may have refreshed while we waited.
	if !force && !stored.Expired() {
		c.token = stored
		return nil
	}
	if c.flowCfg.ClientID == "" {
		return fmt.Errorf("missing Google OAuth client id; reconnect with gg connect google --client-id ID")
	}
	tok, err := connector.RefreshToken(ctx, c.flowCfg, stored.RefreshToken)
	if err != nil {
		return err
	}
	// Generation check: skip the save when the connection changed under us.
	current, err := c.store.Load(Name)
	if err != nil {
		return fmt.Errorf("connector %s was disconnected during refresh; run gg connect %s again", Name, Name)
	}
	if current.RefreshToken != stored.RefreshToken {
		// Re-authorized concurrently: adopt the newer token, never
		// overwrite it with this stale refresh.
		c.token = current
		return nil
	}
	// Keep what the refresh response does not carry.
	if len(tok.Scopes) == 0 {
		tok.Scopes = stored.Scopes
	}
	tok.ClientID = stored.ClientID
	tok.ClientSecret = stored.ClientSecret
	if err := c.store.Save(Name, tok); err != nil {
		return fmt.Errorf("save refreshed token: %w", err)
	}
	c.token = tok
	return nil
}

// forceRefresh hits the token endpoint regardless of the stored expiry
// (used after a 401).
func (c *Client) forceRefresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshLocked(ctx, true)
}

type authTransport struct {
	client *Client
	base   http.RoundTripper
}

func (t *authTransport) baseRT() http.RoundTripper {
	if t.base != nil {
		return t.base
	}
	return http.DefaultTransport
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.client.ensureToken(req.Context()); err != nil {
		return nil, err
	}
	t.client.mu.Lock()
	access := t.client.token.AccessToken
	t.client.mu.Unlock()

	send := func(token string) (*http.Response, error) {
		r2 := req.Clone(req.Context())
		// Clone keeps the (already consumed) body; rebuild it for the retry.
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			r2.Body = body
		}
		r2.Header.Set("Authorization", "Bearer "+token)
		return t.baseRT().RoundTrip(r2)
	}
	resp, err := send(access)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	resp.Body.Close()
	// One retry with a forced refresh: the stored token may look unexpired
	// yet already be rejected, so bypass the expiry check.
	if err := t.client.forceRefresh(req.Context()); err != nil {
		return nil, err
	}
	t.client.mu.Lock()
	access = t.client.token.AccessToken
	t.client.mu.Unlock()
	return send(access)
}

// Get performs an authenticated GET against a full URL.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

// Do performs an authenticated request.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}
