// Package google implements the gg connector for Google services
// (Gmail + Google Calendar) on top of the connector framework.
// It uses plain net/http against the Google REST APIs; no SDK.
package google

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

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
		if err := c.refreshLocked(ctx); err != nil {
			return err
		}
	}
	return nil
}

// refreshLocked refreshes under a cross-process lock so concurrent
// processes do not double-refresh. The in-process mutex is held too.
func (c *Client) refreshLocked(ctx context.Context) error {
	unlock, err := filelock.Lock(c.store.Dir() + "/google-refresh.lock")
	if err != nil {
		return err
	}
	defer unlock()
	// Another process may have refreshed while we waited.
	if tok, err := c.store.Load(Name); err == nil && !tok.Expired() {
		c.token = tok
		return nil
	}
	tok, err := connector.RefreshToken(ctx, c.flowCfg, c.token.RefreshToken)
	if err != nil {
		return err
	}
	tok.Scopes = Scopes
	if err := c.store.Save(Name, tok); err != nil {
		return fmt.Errorf("save refreshed token: %w", err)
	}
	c.token = tok
	return nil
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
	// One retry with a forced refresh.
	t.client.mu.Lock()
	t.client.token.Expiry = time.Now().Add(-time.Minute)
	t.client.mu.Unlock()
	if err := t.client.ensureToken(req.Context()); err != nil {
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
