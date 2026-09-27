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
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"code":401,"message":"Invalid credentials"}}`))
	})
	c := testClient(t, mux, freshToken())
	_, err := c.Search(context.Background(), "x", 1)
	if err == nil || !strings.Contains(err.Error(), "Invalid credentials") {
		t.Fatalf("expected api error, got %v", err)
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
