package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
)

// mockGoogleClient builds a google client wired to a mock API server.
func mockGoogleClient(t *testing.T, mux *http.ServeMux) *google.Client {
	t.Helper()
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	oldAPI, oldCal := google.APIBase, google.CalBase
	google.APIBase, google.CalBase = api.URL, api.URL
	t.Cleanup(func() { google.APIBase, google.CalBase = oldAPI, oldCal })

	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(google.Name, connector.Token{
		AccessToken: "at", RefreshToken: "rt",
		Expiry: time.Now().Add(time.Hour), Scopes: google.Scopes,
	}); err != nil {
		t.Fatal(err)
	}
	c, err := google.NewClient(store, google.Config{ClientID: "cid", HTTPClient: api.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGmailSearchTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]string{{"id": "m1", "threadId": "t1"}},
		})
	})
	tool := NewGmailSearchTool(mockGoogleClient(t, mux))
	def := tool.Definition()
	if def.Name != "gmail_search" || !strings.Contains(def.Description, "newer_than") {
		t.Fatalf("definition = %+v", def)
	}
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":"newer_than:1d"}`))
	if res.IsError {
		t.Fatalf("error: %v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "m1") {
		t.Fatalf("result = %q", res.Content[0].Text)
	}
}

func TestGmailReadTool(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte("Hi there"))
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": "m1",
			"payload": map[string]any{
				"headers": []map[string]string{
					{"name": "From", "value": "a@b.c"},
					{"name": "Subject", "value": "Test"},
				},
				"body": map[string]string{"data": body},
			},
		})
	})
	tool := NewGmailReadTool(mockGoogleClient(t, mux))
	res := tool.Execute(context.Background(), json.RawMessage(`{"id":"m1"}`))
	if res.IsError {
		t.Fatalf("error: %v", res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "a@b.c") || !strings.Contains(text, "Hi there") {
		t.Fatalf("result = %q", text)
	}
}

func TestGmailSendToolApproval(t *testing.T) {
	mux := http.NewServeMux()
	tool := NewGmailSendTool(mockGoogleClient(t, mux))
	raw := json.RawMessage(`{"to":"x@y.z","subject":"Hello","body":"world"}`)
	req, err := tool.ApprovalRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolName != "gmail_send" || !strings.Contains(req.Summary, "x@y.z") {
		t.Fatalf("approval = %+v", req)
	}
	if !strings.Contains(req.Details, "Hello") {
		t.Fatalf("details = %q", req.Details)
	}
	assertGoogleWriteConsequences(t, req)
}

func assertGoogleWriteConsequences(t *testing.T, req agent.ApprovalRequest) {
	t.Helper()
	if req.Reversible == nil || *req.Reversible || req.ExternallyVisible == nil || !*req.ExternallyVisible ||
		req.SpendsMoneyOrChangesPermissions == nil || *req.SpendsMoneyOrChangesPermissions ||
		req.ActsInUserName == nil || !*req.ActsInUserName || !req.RequiresHumanConfirm() {
		t.Fatalf("expected irreversible E/U without M: %+v", req)
	}
}

func TestGmailSendOrDraft(t *testing.T) {
	for _, tc := range []struct {
		name, option, path, summary, result string
		personal                            bool
	}{
		{"default sends", "", "/gmail/v1/users/me/messages/send", "send email", "sent (id sent1)", false},
		{"explicit send", `,"as_draft":false`, "/gmail/v1/users/me/messages/send", "send email", "sent (id sent1)", false},
		{"draft only", `,"as_draft":true`, "/gmail/v1/users/me/drafts", "save Gmail draft (not sent)", "draft created (id draft1; not sent)", false},
		{"personal default draft", "", "/gmail/v1/users/me/drafts", "save Gmail draft (not sent)", "draft created (id draft1; not sent)", true},
		{"personal explicit send", `,"as_draft":false`, "/gmail/v1/users/me/messages/send", "send email", "sent (id sent1)", true},
		{"personal explicit draft", `,"as_draft":true`, "/gmail/v1/users/me/drafts", "save Gmail draft (not sent)", "draft created (id draft1; not sent)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != tc.path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Path == "/gmail/v1/users/me/drafts" {
					w.Write([]byte(`{"id":"draft1","message":{"id":"message1"}}`))
				} else {
					w.Write([]byte(`{"id":"sent1"}`))
				}
			})
			tool := NewGmailSendTool(mockGoogleClient(t, mux))
			tool.DraftByDefault = tc.personal
			raw := json.RawMessage(`{"to":"x@y.z","subject":"Hello","body":"world"` + tc.option + `}`)
			req, err := tool.ApprovalRequest(raw)
			if err != nil || !strings.Contains(req.Summary, tc.summary) || string(req.Arguments) != string(raw) {
				t.Fatalf("approval = %+v, %v", req, err)
			}
			assertGoogleWriteConsequences(t, req)
			res := tool.Execute(context.Background(), raw)
			if res.IsError || res.Content[0].Text != tc.result || calls != 1 {
				t.Fatalf("result = %+v, calls = %d", res, calls)
			}
		})
	}
}

func TestGmailDraftFailureNeverSends(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/gmail/v1/users/me/drafts" {
			t.Errorf("draft failure must not fall back to send: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"code":403,"message":"insufficient scopes"}}`))
	})
	tool := NewGmailSendTool(mockGoogleClient(t, mux))
	res := tool.Execute(context.Background(), json.RawMessage(`{"to":"x@y.z","subject":"Hello","body":"world","as_draft":true}`))
	if !res.IsError || calls != 1 || !strings.Contains(res.Content[0].Text, "not sent") || !strings.Contains(res.Content[0].Text, "gg connect google --force") {
		t.Fatalf("result = %+v, calls = %d", res, calls)
	}
}

func TestCalendarAgendaTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"id":      "e1",
				"summary": "Standup",
				"start":   map[string]string{"dateTime": "2026-09-28T10:00:00+08:00"},
				"end":     map[string]string{"dateTime": "2026-09-28T10:30:00+08:00"},
			}},
		})
	})
	loc := time.FixedZone("CST", 8*3600)
	tool := NewCalendarAgendaTool(mockGoogleClient(t, mux), loc, nil)
	res := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.IsError {
		t.Fatalf("error: %v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "Standup") {
		t.Fatalf("result = %q", res.Content[0].Text)
	}
}

func TestCalendarCreateToolValidation(t *testing.T) {
	mux := http.NewServeMux()
	loc := time.FixedZone("CST", 8*3600)
	tool := NewCalendarCreateTool(mockGoogleClient(t, mux), loc, nil)
	// End before start is rejected without hitting the API.
	res := tool.Execute(context.Background(),
		json.RawMessage(`{"title":"x","start":"2026-09-28 16:00","end":"2026-09-28 15:00"}`))
	if !res.IsError {
		t.Fatal("expected error for end before start")
	}
	res = tool.Execute(context.Background(),
		json.RawMessage(`{"title":"x","start":"garbage","end":"2026-09-28 15:00"}`))
	if !res.IsError {
		t.Fatal("expected error for bad datetime")
	}
	raw := json.RawMessage(`{"title":"1:1","start":"2026-09-28 15:00","end":"2026-09-28 16:00"}`)
	req, err := tool.ApprovalRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Summary, "1:1") || !strings.Contains(req.Summary, "2026-09-28 15:00") {
		t.Fatalf("approval = %+v", req)
	}
	assertGoogleWriteConsequences(t, req)
}

func TestConnectorToolsNotConnected(t *testing.T) {
	// A client whose store has no token surfaces the reconnect hint.
	store, err := connector.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := google.NewClient(store, google.Config{ClientID: "cid"})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewGmailSearchTool(c)
	res := tool.Execute(context.Background(), json.RawMessage(`{"query":"x"}`))
	if !res.IsError {
		t.Fatal("expected error when not connected")
	}
	if !strings.Contains(res.Content[0].Text, "gg connect") {
		t.Fatalf("result = %q (want reconnect hint)", res.Content[0].Text)
	}
}

func TestAgendaFormatting(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"id": "e1", "summary": "Holiday",
					"start": map[string]string{"date": "2026-09-29"},
					"end":   map[string]string{"date": "2026-09-30"},
				},
				{
					"id": "e2", "summary": "Deploy",
					"start": map[string]string{"dateTime": "2026-09-28T23:00:00+08:00"},
					"end":   map[string]string{"dateTime": "2026-09-29T01:00:00+08:00"},
				},
			},
		})
	})
	loc := time.FixedZone("CST", 8*3600)
	tool := NewCalendarAgendaTool(mockGoogleClient(t, mux), loc, nil)
	res := tool.Execute(context.Background(), json.RawMessage(`{"days":3}`))
	if res.IsError {
		t.Fatalf("error: %v", res.Content)
	}
	text := res.Content[0].Text
	// All-day events carry their date; overnight events show the end date.
	if !strings.Contains(text, "Holiday") || !strings.Contains(text, "2026-09-29") {
		t.Fatalf("all-day missing date: %q", text)
	}
	if !strings.Contains(text, "09-29 01:00") {
		t.Fatalf("overnight missing end date: %q", text)
	}
}

func TestCalendarInvalidTimezone(t *testing.T) {
	mux := http.NewServeMux()
	loc := time.FixedZone("CST", 8*3600)
	tool := NewCalendarAgendaTool(mockGoogleClient(t, mux), loc,
		errors.New(`profile timezone "UTC+8" is not a valid IANA name`))
	res := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("expected error for invalid timezone")
	}
	if !strings.Contains(res.Content[0].Text, "UTC+8") {
		t.Fatalf("result = %q", res.Content[0].Text)
	}
}
