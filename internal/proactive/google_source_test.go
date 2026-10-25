package proactive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
)

type fakeGoogleReader struct {
	search func(context.Context, string, int) ([]google.MessageSummary, error)
	read   func(context.Context, string) (google.Message, error)
	agenda func(context.Context, time.Time, int) ([]google.Event, error)
}

func (f fakeGoogleReader) Search(ctx context.Context, query string, max int) ([]google.MessageSummary, error) {
	return f.search(ctx, query, max)
}
func (f fakeGoogleReader) Read(ctx context.Context, id string) (google.Message, error) {
	return f.read(ctx, id)
}
func (f fakeGoogleReader) Agenda(ctx context.Context, from time.Time, days int) ([]google.Event, error) {
	return f.agenda(ctx, from, days)
}

func TestGoogleDigestEmailSummary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	source := googleDigestSource{client: fakeGoogleReader{
		search: func(gotCtx context.Context, query string, max int) ([]google.MessageSummary, error) {
			if gotCtx != ctx || query != "is:unread newer_than:2d" || max != 5 {
				t.Fatalf("search ctx=%v query=%q max=%d", gotCtx, query, max)
			}
			return []google.MessageSummary{{ID: "first"}, {ID: "second"}, {ID: "third"}, {ID: "fourth"}, {ID: "fifth"}, {ID: "over-limit"}}, nil
		},
		read: func(gotCtx context.Context, id string) (google.Message, error) {
			reads++
			if gotCtx != ctx || id == "over-limit" {
				t.Fatalf("unexpected read: ctx=%v id=%s", gotCtx, id)
			}
			if id == "first" {
				return google.Message{From: "alex@example.com", Subject: "Planning\nnotes", Date: "Sun, 25 Oct 2026", Snippet: "Short preview", Body: "Full body should be omitted"}, nil
			}
			return google.Message{From: strings.Repeat("寄", 300), Subject: id, Body: strings.Repeat("文", 600)}, nil
		},
	}}
	body, err := source.EmailSummary(ctx, time.Now())
	if err != nil || reads != 5 || !utf8.ValidString(body) || len([]rune(body)) > 6000 {
		t.Fatalf("reads=%d body length=%d err=%v", reads, len([]rune(body)), err)
	}
	for _, want := range []string{"From: alex@example.com", "Subject: Planning notes", "Date: Sun, 25 Oct 2026", "Short preview", strings.Repeat("文", 500) + "…"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in digest", want)
		}
	}
	for _, unwanted := range []string{"Full body should be omitted", "over-limit", strings.Repeat("文", 501), strings.Repeat("寄", 201)} {
		if strings.Contains(body, unwanted) {
			t.Errorf("unexpected %q in digest", unwanted)
		}
	}
}

func TestGoogleDigestCalendarReminder(t *testing.T) {
	loc := time.FixedZone("local-test", 8*60*60)
	now := time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC)
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := googleDigestSource{loc: loc, client: fakeGoogleReader{
		agenda: func(gotCtx context.Context, from time.Time, days int) ([]google.Event, error) {
			if gotCtx != ctx || !from.Equal(start) || from.Location() != loc || days != 1 {
				t.Fatalf("agenda ctx=%v from=%v days=%d", gotCtx, from, days)
			}
			events := []google.Event{
				{Summary: "Holiday", AllDay: true, Start: start},
				{Summary: "Planning", Start: start.Add(10 * time.Hour).UTC(), End: start.Add(11 * time.Hour).UTC(), Location: "Room A\nupstairs"},
			}
			for range 20 {
				events = append(events, google.Event{Summary: strings.Repeat("会", 300), AllDay: true})
			}
			return events, nil
		},
	}}
	body, err := source.CalendarReminder(ctx, now)
	if err != nil || len([]rune(body)) > 10000 {
		t.Fatalf("body=%s err=%v", body, err)
	}
	for _, want := range []string{"Today's agenda (2026-10-25, local-test)", "All day: Holiday", "10:00–11:00: Planning (Room A upstairs)", "2 more events omitted"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, strings.Repeat("会", 201)) {
		t.Fatal("unbounded event summary")
	}
}

func TestGoogleDigestEmpty(t *testing.T) {
	source := googleDigestSource{loc: time.UTC, client: fakeGoogleReader{
		search: func(context.Context, string, int) ([]google.MessageSummary, error) { return nil, nil },
		agenda: func(context.Context, time.Time, int) ([]google.Event, error) { return nil, nil },
	}}
	inbox, err := source.EmailSummary(context.Background(), time.Now())
	if err != nil || !strings.Contains(inbox, "No unread mail") {
		t.Fatalf("inbox=%q err=%v", inbox, err)
	}
	agenda, err := source.CalendarReminder(context.Background(), time.Now())
	if err != nil || !strings.Contains(agenda, "No events today") {
		t.Fatalf("agenda=%q err=%v", agenda, err)
	}
}

func TestGoogleDigestReadErrors(t *testing.T) {
	want := errors.New("Google read unavailable")
	for _, op := range []string{"search", "read", "agenda"} {
		t.Run(op, func(t *testing.T) {
			source := googleDigestSource{loc: time.UTC, client: fakeGoogleReader{
				search: func(context.Context, string, int) ([]google.MessageSummary, error) {
					if op == "search" {
						return nil, want
					}
					return []google.MessageSummary{{ID: "message"}}, nil
				},
				read:   func(context.Context, string) (google.Message, error) { return google.Message{}, want },
				agenda: func(context.Context, time.Time, int) ([]google.Event, error) { return nil, want },
			}}
			var body string
			var err error
			if op == "agenda" {
				body, err = source.CalendarReminder(context.Background(), time.Now())
			} else {
				body, err = source.EmailSummary(context.Background(), time.Now())
			}
			if body != "" || !errors.Is(err, want) || !strings.Contains(err.Error(), "gg connect google --force") {
				t.Fatalf("body=%q err=%v", body, err)
			}
		})
	}
}

func TestNewGoogleDigestSource(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		scopes     []string
	}{
		{name: "not connected", want: "google not connected"},
		{name: "missing Gmail", scopes: []string{google.ScopeCalendar}, want: google.ScopeGmailRead},
		{name: "missing Calendar", scopes: []string{google.ScopeGmailRead}, want: "calendar.events.readonly"},
		{name: "unknown scopes", scopes: []string{"unknown"}, want: "missing required scope(s)"},
		{name: "existing connector grant", scopes: []string{google.ScopeGmailRead, google.ScopeCalendar}},
		{name: "read-only events grant", scopes: []string{google.ScopeGmailRead, "https://www.googleapis.com/auth/calendar.events.readonly"}},
		{name: "read-only calendar grant", scopes: []string{google.ScopeGmailRead, "https://www.googleapis.com/auth/calendar.readonly"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A custom connector directory must be honored; no default-home lookup.
			cfg := config.Config{HomeDir: t.TempDir(), Connectors: config.ConnectorConfig{Dir: t.TempDir()}}
			store, err := connector.Open(cfg.Connectors.Dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.scopes != nil {
				// Deliberately expired: construction must not refresh or hit a network.
				if err := store.Save(google.Name, connector.Token{AccessToken: "fake", RefreshToken: "fake", Scopes: tc.scopes}); err != nil {
					t.Fatal(err)
				}
			}
			source, err := NewGoogleDigestSource(cfg)
			if tc.want != "" {
				if err == nil || source != nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "gg connect google") {
					t.Fatalf("source=%v err=%v", source, err)
				}
				if tc.scopes == nil && !errors.Is(err, connector.ErrNotConnected) {
					t.Fatalf("lost ErrNotConnected: %v", err)
				}
				return
			}
			if err != nil || source == nil {
				t.Fatalf("source=%v err=%v", source, err)
			}
			if _, ok := source.(*googleDigestSource).client.(*google.Client); !ok {
				t.Fatalf("source did not construct Google client: %T", source)
			}
		})
	}
}

func TestNewGoogleDigestSourceStoreFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "google.json"), []byte("invalid JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "google.json")} {
		source, err := NewGoogleDigestSource(config.Config{Connectors: config.ConnectorConfig{Dir: path}})
		if err == nil || source != nil || !strings.Contains(err.Error(), "gg connect google") {
			t.Fatalf("source=%v err=%v", source, err)
		}
	}
}
