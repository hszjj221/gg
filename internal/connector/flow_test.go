package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mockTokenServer returns a server implementing the token endpoint and
// records the last form it received.
func mockTokenServer(t *testing.T, body string) (*httptest.Server, *url.Values) {
	t.Helper()
	var last url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		last = r.Form
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestExchange(t *testing.T) {
	srv, last := mockTokenServer(t, `{"access_token":"at1","refresh_token":"rt1","expires_in":3600,"scope":"a b","token_type":"Bearer"}`)
	cfg := FlowConfig{
		TokenURL:     srv.URL,
		ClientID:     "cid",
		ClientSecret: "csec",
	}
	tok, err := exchange(context.Background(), cfg, "the-code", "verifier-xyz", "http://127.0.0.1:1/callback")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at1" || tok.RefreshToken != "rt1" {
		t.Fatalf("token = %+v", tok)
	}
	if len(tok.Scopes) != 2 || tok.Scopes[0] != "a" {
		t.Fatalf("scopes = %v", tok.Scopes)
	}
	if time.Until(tok.Expiry) < 50*time.Minute {
		t.Fatalf("expiry too soon: %v", tok.Expiry)
	}
	// PKCE + client credentials must be in the form.
	if (*last).Get("grant_type") != "authorization_code" {
		t.Fatalf("grant_type = %q", (*last).Get("grant_type"))
	}
	if (*last).Get("code") != "the-code" || (*last).Get("code_verifier") != "verifier-xyz" {
		t.Fatalf("code/verifier missing: %v", *last)
	}
	if (*last).Get("client_id") != "cid" || (*last).Get("client_secret") != "csec" {
		t.Fatalf("client credentials missing: %v", *last)
	}
	if (*last).Get("redirect_uri") != "http://127.0.0.1:1/callback" {
		t.Fatalf("redirect_uri = %q", (*last).Get("redirect_uri"))
	}
}

func TestExchangeError(t *testing.T) {
	srv, _ := mockTokenServer(t, `{"error":"invalid_grant","error_description":"bad code"}`)
	cfg := FlowConfig{TokenURL: srv.URL, ClientID: "cid"}
	if _, err := exchange(context.Background(), cfg, "bad", "v", "http://x"); err == nil {
		t.Fatal("expected error for invalid_grant")
	} else if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error = %v", err)
	}
}

func TestRefreshToken(t *testing.T) {
	srv, last := mockTokenServer(t, `{"access_token":"at2","expires_in":3600,"token_type":"Bearer"}`)
	cfg := FlowConfig{TokenURL: srv.URL, ClientID: "cid"}
	tok, err := RefreshToken(context.Background(), cfg, "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at2" {
		t.Fatalf("token = %+v", tok)
	}
	// Providers may omit a new refresh token; the old one is kept.
	if tok.RefreshToken != "old-refresh" {
		t.Fatalf("refresh token not preserved: %q", tok.RefreshToken)
	}
	if (*last).Get("grant_type") != "refresh_token" || (*last).Get("refresh_token") != "old-refresh" {
		t.Fatalf("form = %v", *last)
	}
}

func TestRefreshTokenError(t *testing.T) {
	srv, _ := mockTokenServer(t, `{"error":"invalid_grant","error_description":"revoked"}`)
	cfg := FlowConfig{TokenURL: srv.URL, ClientID: "cid"}
	_, err := RefreshToken(context.Background(), cfg, "dead")
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("expected invalid_grant, got %v", err)
	}
}

func TestRunFlowManual(t *testing.T) {
	srv, _ := mockTokenServer(t, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"scope":"x"}`)
	var opened string
	// The pasted URL must carry the flow's state; capture it from the auth URL.
	var authQuery url.Values
	cfg := FlowConfig{
		AuthURL:  "https://auth.example/authorize",
		TokenURL: srv.URL,
		ClientID: "cid",
		Scopes:   []string{"x"},
		Manual:   true,
		OpenBrowser: func(u string) {
			opened = u
			parsed, _ := url.Parse(u)
			authQuery = parsed.Query()
		},
		ReadCode: func() (string, error) {
			return "http://127.0.0.1:9/callback?code=pasted-code&state=" + authQuery.Get("state"), nil
		},
	}
	tok, err := RunFlow(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" {
		t.Fatalf("token = %+v", tok)
	}
	if !strings.HasPrefix(opened, "https://auth.example/authorize?") {
		t.Fatalf("auth url = %q", opened)
	}
	q := authQuery
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("PKCE missing in auth url: %v", q)
	}
	if q.Get("state") == "" || q.Get("redirect_uri") == "" {
		t.Fatalf("state/redirect missing: %v", q)
	}
	// Google retired OOB; manual mode must not use it.
	if strings.Contains(q.Get("redirect_uri"), "oob") {
		t.Fatalf("manual flow must not use OOB redirect: %q", q.Get("redirect_uri"))
	}
}

func TestParseManualRedirect(t *testing.T) {
	code, err := parseManualRedirect("http://127.0.0.1:9/callback?code=abc&state=s1", "s1")
	if err != nil || code != "abc" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if _, err := parseManualRedirect("http://127.0.0.1:9/callback?code=abc&state=other", "s1"); err == nil {
		t.Fatal("expected state mismatch error")
	}
	if _, err := parseManualRedirect("http://127.0.0.1:9/callback?error=access_denied&state=s1", "s1"); err == nil {
		t.Fatal("expected authorization error")
	}
	if _, err := parseManualRedirect("http://127.0.0.1:9/callback?state=s1", "s1"); err == nil {
		t.Fatal("expected missing code error")
	}
}

func TestRunFlowRequiresClientID(t *testing.T) {
	if _, err := RunFlow(context.Background(), FlowConfig{}); err == nil {
		t.Fatal("expected error without client id")
	}
}
