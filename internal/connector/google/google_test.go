package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/connector"
)

// testClient builds a google Client against mock API + token servers.
func testClient(t *testing.T, mux *http.ServeMux, tok connector.Token) *Client {
	t.Helper()
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	oldAPI, oldCal := APIBase, CalBase
	APIBase, CalBase = api.URL, api.URL
	t.Cleanup(func() { APIBase, CalBase = oldAPI, oldCal })

	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Name, tok); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(store, Config{ClientID: "cid", HTTPClient: api.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func freshToken() connector.Token {
	return connector.Token{
		AccessToken:  "at",
		RefreshToken: "rt",
		Expiry:       time.Now().Add(time.Hour),
		Scopes:       Scopes,
	}
}

func TestSearch(t *testing.T) {
	var gotQ, gotMax string
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		gotQ, gotMax = r.URL.Query().Get("q"), r.URL.Query().Get("maxResults")
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]string{{"id": "m1", "threadId": "t1"}, {"id": "m2", "threadId": "t2"}},
		})
	})
	c := testClient(t, mux, freshToken())
	msgs, err := c.Search(context.Background(), "newer_than:1d", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ID != "m1" {
		t.Fatalf("msgs = %+v", msgs)
	}
	if gotQ != "newer_than:1d" || gotMax != "5" {
		t.Fatalf("q=%q max=%q", gotQ, gotMax)
	}
	if gotAuth != "Bearer at" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

func TestSearchEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	c := testClient(t, mux, freshToken())
	msgs, err := c.Search(context.Background(), "nomatch", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestRead(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte("Hello world"))
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "format=full") {
			t.Errorf("missing format=full: %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "m1",
			"snippet": "Hello world",
			"payload": map[string]any{
				"headers": []map[string]string{
					{"name": "From", "value": "boss@example.com"},
					{"name": "Subject", "value": "Q3 planning"},
					{"name": "Date", "value": "Sat, 26 Sep 2026 10:00:00 +0800"},
				},
				"parts": []map[string]any{
					{"mimeType": "text/plain", "body": map[string]string{"data": body}},
					{"mimeType": "text/html", "body": map[string]string{"data": "aGk="}},
				},
			},
		})
	})
	c := testClient(t, mux, freshToken())
	msg, err := c.Read(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.From != "boss@example.com" || msg.Subject != "Q3 planning" {
		t.Fatalf("msg = %+v", msg)
	}
	if msg.Body != "Hello world" {
		t.Fatalf("body = %q", msg.Body)
	}
}

func TestSend(t *testing.T) {
	var gotRaw string
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/send", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Raw string `json:"raw"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		gotRaw = in.Raw
		json.NewEncoder(w).Encode(map[string]string{"id": "sent1"})
	})
	c := testClient(t, mux, freshToken())
	id, err := c.Send(context.Background(), "a@b.c", "Hi", "body text")
	if err != nil {
		t.Fatal(err)
	}
	if id != "sent1" {
		t.Fatalf("id = %q", id)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(gotRaw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(decoded)
	if !strings.Contains(s, "To: a@b.c") || !strings.Contains(s, "Subject: Hi") || !strings.Contains(s, "body text") {
		t.Fatalf("raw message = %q", s)
	}
}

func TestAgenda(t *testing.T) {
	var gotMin, gotMax string
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(map[string]string{"id": "ev1"})
			return
		}
		gotMin, gotMax = r.URL.Query().Get("timeMin"), r.URL.Query().Get("timeMax")
		json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"id":      "e1",
					"summary": "Standup",
					"start":   map[string]string{"dateTime": "2026-09-28T10:00:00+08:00"},
					"end":     map[string]string{"dateTime": "2026-09-28T10:30:00+08:00"},
				},
				{
					"id":      "e2",
					"summary": "Holiday",
					"start":   map[string]string{"date": "2026-09-29"},
					"end":     map[string]string{"date": "2026-09-30"},
				},
			},
		})
	})
	c := testClient(t, mux, freshToken())
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	events, err := c.Agenda(context.Background(), from, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Summary != "Standup" || events[0].AllDay {
		t.Fatalf("event0 = %+v", events[0])
	}
	if !events[1].AllDay || events[1].Summary != "Holiday" {
		t.Fatalf("event1 = %+v", events[1])
	}
	if gotMin == "" || gotMax == "" {
		t.Fatalf("time window not sent: %q %q", gotMin, gotMax)
	}
}

func TestCreateEvent(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]string{"id": "ev9"})
	})
	c := testClient(t, mux, freshToken())
	loc := time.FixedZone("CST", 8*3600)
	start := time.Date(2026, 9, 28, 15, 0, 0, 0, loc)
	end := start.Add(time.Hour)
	id, err := c.CreateEvent(context.Background(), "1:1", start, end, "desc", "room 3")
	if err != nil {
		t.Fatal(err)
	}
	if id != "ev9" {
		t.Fatalf("id = %q", id)
	}
	if got["summary"] != "1:1" || got["location"] != "room 3" {
		t.Fatalf("payload = %v", got)
	}
	startSent := got["start"].(map[string]any)["dateTime"].(string)
	if !strings.HasPrefix(startSent, "2026-09-28T15:00:00+08:00") {
		t.Fatalf("start = %q", startSent)
	}
}

func TestAutoRefresh(t *testing.T) {
	refreshCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-new", "expires_in": 3600, "token_type": "Bearer",
		})
	})
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"messages":[]}`))
	})
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	oldAPI, oldCal := APIBase, CalBase
	APIBase, CalBase = api.URL, api.URL
	t.Cleanup(func() { APIBase, CalBase = oldAPI, oldCal })

	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Expired access token forces a refresh on first use.
	stale := connector.Token{
		AccessToken: "at-old", RefreshToken: "rt", Expiry: time.Now().Add(-time.Hour), Scopes: Scopes,
	}
	if err := store.Save(Name, stale); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(store, Config{
		ClientID:   "cid",
		TokenURL:   api.URL + "/token",
		HTTPClient: api.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "x", 1); err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	// The refreshed token was persisted.
	tok, err := store.Load(Name)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at-new" {
		t.Fatalf("persisted token = %+v", tok)
	}
}

func TestAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":403,"message":"Insufficient permission"}}`))
	})
	c := testClient(t, mux, freshToken())
	_, err := c.Search(context.Background(), "x", 1)
	if err == nil || !strings.Contains(err.Error(), "Insufficient permission") {
		t.Fatalf("expected api error, got %v", err)
	}
}

func TestAPIError401RefreshFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"revoked"}`))
	})
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"code":401,"message":"Invalid credentials"}}`))
	})
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	oldAPI, oldCal := APIBase, CalBase
	APIBase, CalBase = api.URL, api.URL
	t.Cleanup(func() { APIBase, CalBase = oldAPI, oldCal })

	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tok := freshToken()
	tok.ClientID = "cid"
	if err := store.Save(Name, tok); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(store, Config{TokenURL: api.URL + "/token", HTTPClient: api.Client()})
	if err != nil {
		t.Fatal(err)
	}
	// 401 triggers a forced refresh; the revoked refresh token surfaces
	// invalid_grant instead of retrying with the dead token.
	_, err = c.Search(context.Background(), "x", 1)
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("expected invalid_grant, got %v", err)
	}
}

func TestParseDateTime(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	tm, err := ParseDateTime("2026-09-28 15:04", loc)
	if err != nil {
		t.Fatal(err)
	}
	if tm.Hour() != 15 || tm.Format("2006-01-02") != "2026-09-28" {
		t.Fatalf("parsed = %v", tm)
	}
	if _, err := ParseDateTime("tomorrow", loc); err == nil {
		t.Fatal("expected error for garbage input")
	}
}

func TestReadNestedMIME(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte("nested hello"))
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		// multipart/mixed -> multipart/alternative -> text/plain
		json.NewEncoder(w).Encode(map[string]any{
			"id": "m1",
			"payload": map[string]any{
				"headers":  []map[string]string{{"name": "Subject", "value": "nested"}},
				"mimeType": "multipart/mixed",
				"parts": []map[string]any{
					{
						"mimeType": "multipart/alternative",
						"parts": []map[string]any{
							{"mimeType": "text/plain", "body": map[string]string{"data": body}},
							{"mimeType": "text/html", "body": map[string]string{"data": "aGk="}},
						},
					},
					{"mimeType": "application/pdf", "body": map[string]string{"data": "e30="}},
				},
			},
		})
	})
	c := testClient(t, mux, freshToken())
	msg, err := c.Read(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != "nested hello" {
		t.Fatalf("body = %q", msg.Body)
	}
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	mux := http.NewServeMux()
	c := testClient(t, mux, freshToken())
	if _, err := c.Send(context.Background(), "a@b.c\r\nBcc: evil@x.y", "s", "b"); err == nil {
		t.Fatal("expected error for CRLF in To")
	}
	if _, err := c.Send(context.Background(), "a@b.c", "s\r\nX-Extra: 1", "b"); err == nil {
		t.Fatal("expected error for CRLF in Subject")
	}
	if _, err := c.Send(context.Background(), "not-an-address", "s", "b"); err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestForceRefreshOn401(t *testing.T) {
	refreshCalls := 0
	sawBearer := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-fresh", "expires_in": 3600, "token_type": "Bearer",
		})
	})
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		sawBearer = r.Header.Get("Authorization")
		if strings.Contains(sawBearer, "at-stale") {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":401,"message":"Invalid credentials"}}`))
			return
		}
		w.Write([]byte(`{"messages":[]}`))
	})
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	oldAPI, oldCal := APIBase, CalBase
	APIBase, CalBase = api.URL, api.URL
	t.Cleanup(func() { APIBase, CalBase = oldAPI, oldCal })

	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Token looks UNEXPIRED but is rejected by the API: the 401 path must
	// force a real refresh, not reuse the rejected token.
	stale := connector.Token{
		AccessToken: "at-stale", RefreshToken: "rt",
		Expiry: time.Now().Add(time.Hour), Scopes: Scopes,
		ClientID: "cid",
	}
	if err := store.Save(Name, stale); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(store, Config{TokenURL: api.URL + "/token", HTTPClient: api.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(context.Background(), "x", 1); err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if !strings.Contains(sawBearer, "at-fresh") {
		t.Fatalf("retry did not use the fresh token: %q", sawBearer)
	}
}

func TestRefreshDoesNotResurrectAfterDisconnect(t *testing.T) {
	mux := http.NewServeMux()
	release := make(chan struct{})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the refresh HTTP in flight
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-new", "expires_in": 3600, "token_type": "Bearer",
		})
	})
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)

	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stale := connector.Token{
		AccessToken: "at-old", RefreshToken: "rt",
		Expiry: time.Now().Add(-time.Hour), Scopes: Scopes, ClientID: "cid",
	}
	if err := store.Save(Name, stale); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(store, Config{TokenURL: api.URL + "/token", HTTPClient: api.Client()})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.forceRefresh(context.Background()) }()
	time.Sleep(200 * time.Millisecond)         // let the refresh HTTP start
	if err := store.Remove(Name); err != nil { // user disconnects mid-refresh
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("expected error when token was removed mid-refresh")
	}
	if _, err := store.Load(Name); err == nil {
		t.Fatal("refresh resurrected the disconnected token")
	}
}

func TestNewClientFallsBackToStoredCredentials(t *testing.T) {
	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Name, connector.Token{
		AccessToken: "at", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour),
		ClientID: "persisted-id", ClientSecret: "persisted-secret",
	}); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(store, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if c.flowCfg.ClientID != "persisted-id" || c.flowCfg.ClientSecret != "persisted-secret" {
		t.Fatalf("flowCfg = %+v", c.flowCfg)
	}
	// Explicit config still wins.
	c2, err := NewClient(store, Config{ClientID: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	if c2.flowCfg.ClientID != "explicit" {
		t.Fatalf("flowCfg = %+v", c2.flowCfg)
	}
}

func TestAgendaPagination(t *testing.T) {
	pages := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		pages++
		out := map[string]any{
			"items": []map[string]any{{
				"id": "e1", "summary": "page",
				"start": map[string]string{"dateTime": "2026-09-28T10:00:00+08:00"},
				"end":   map[string]string{"dateTime": "2026-09-28T11:00:00+08:00"},
			}},
		}
		if r.URL.Query().Get("pageToken") == "" {
			out["nextPageToken"] = "tok2"
		}
		json.NewEncoder(w).Encode(out)
	})
	c := testClient(t, mux, freshToken())
	events, err := c.Agenda(context.Background(), time.Now(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || pages != 2 {
		t.Fatalf("events=%d pages=%d", len(events), pages)
	}
}

func TestAgendaDaysClamp(t *testing.T) {
	var gotMin, gotMax string
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		gotMin, gotMax = r.URL.Query().Get("timeMin"), r.URL.Query().Get("timeMax")
		w.Write([]byte(`{"items":[]}`))
	})
	c := testClient(t, mux, freshToken())
	from := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if _, err := c.Agenda(context.Background(), from, 99); err != nil {
		t.Fatal(err)
	}
	min, _ := time.Parse(time.RFC3339, gotMin)
	max, _ := time.Parse(time.RFC3339, gotMax)
	if max.Sub(min) != 30*24*time.Hour {
		t.Fatalf("window = %v to %v, want 30 days", min, max)
	}
}
