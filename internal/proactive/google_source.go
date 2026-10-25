package proactive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
)

// googleDigestReader deliberately exposes only the three read operations.
type googleDigestReader interface {
	Search(context.Context, string, int) ([]google.MessageSummary, error)
	Read(context.Context, string) (google.Message, error)
	Agenda(context.Context, time.Time, int) ([]google.Event, error)
}

type googleDigestSource struct {
	client googleDigestReader
	loc    *time.Location
}

// NewGoogleDigestSource validates the stored connection and read scopes without
// contacting Google. The client refreshes tokens on demand during live reads.
// No runtime, provider, tool registry, or outbound delivery is involved.
func NewGoogleDigestSource(cfg config.Config) (DigestSource, error) {
	store, err := connector.Open(cfg.Connectors.Dir)
	if err != nil {
		return nil, fmt.Errorf("open Google digest connection; run: gg connect google: %w", err)
	}
	tok, err := store.Load(google.Name)
	if err != nil {
		if errors.Is(err, connector.ErrNotConnected) {
			return nil, fmt.Errorf("google not connected; run: gg connect google: %w", err)
		}
		return nil, fmt.Errorf("load Google digest connection; run: gg connect google: %w", err)
	}
	hasScope := func(want string) bool {
		for _, scope := range tok.Scopes {
			if scope == want {
				return true
			}
		}
		return false
	}
	var missing []string
	if !hasScope(google.ScopeGmailRead) {
		missing = append(missing, google.ScopeGmailRead)
	}
	// Accept the existing connector's events scope and equivalent read grants.
	if !hasScope(google.ScopeCalendar) &&
		!hasScope("https://www.googleapis.com/auth/calendar.events.readonly") &&
		!hasScope("https://www.googleapis.com/auth/calendar.readonly") &&
		!hasScope("https://www.googleapis.com/auth/calendar") {
		missing = append(missing, "https://www.googleapis.com/auth/calendar.events.readonly (or "+google.ScopeCalendar+")")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("google digest missing required scope(s): %s; reconnect: gg connect google --force", strings.Join(missing, ", "))
	}
	client, err := google.NewClient(store, google.Config{
		ClientID: cfg.Connectors.Google.ClientID, ClientSecret: cfg.Connectors.Google.ClientSecret,
	})
	if err != nil {
		return nil, fmt.Errorf("create Google digest client; run: gg connect google: %w", err)
	}
	return &googleDigestSource{client: client, loc: time.Local}, nil
}

const digestMailLimit = 5
const digestEventLimit = 20

func (s *googleDigestSource) EmailSummary(ctx context.Context, _ time.Time) (string, error) {
	messages, err := s.client.Search(ctx, "is:unread newer_than:2d", digestMailLimit)
	if err != nil {
		return "", fmt.Errorf("Gmail search failed; check connection/scopes with gg connect google --force: %w", err)
	}
	if len(messages) == 0 {
		return "No unread mail from the last two days.", nil
	}
	var body strings.Builder
	body.WriteString("Unread mail from the last two days (up to 5 messages):\n")
	for i, summary := range messages {
		if i == digestMailLimit {
			break
		}
		msg, err := s.client.Read(ctx, summary.ID)
		if err != nil {
			return "", fmt.Errorf("Gmail read failed; check connection/scopes with gg connect google --force: %w", err)
		}
		preview := msg.Snippet
		if strings.TrimSpace(preview) == "" {
			preview = msg.Body
		}
		fmt.Fprintf(&body, "\nFrom: %s\nSubject: %s\nDate: %s\n%s\n",
			digestText(msg.From, 200), digestText(msg.Subject, 200), digestText(msg.Date, 100), digestText(preview, 500))
	}
	return strings.TrimSpace(body.String()), nil
}

func (s *googleDigestSource) CalendarReminder(ctx context.Context, now time.Time) (string, error) {
	local := now.In(s.loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
	events, err := s.client.Agenda(ctx, start, 1)
	if err != nil {
		return "", fmt.Errorf("Calendar agenda failed; check connection/scopes with gg connect google --force: %w", err)
	}
	if len(events) == 0 {
		return "No events today (" + start.Format("2006-01-02") + ").", nil
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Today's agenda (%s, %s):\n", start.Format("2006-01-02"), s.loc)
	for i, event := range events {
		if i == digestEventLimit {
			fmt.Fprintf(&body, "… %d more events omitted.\n", len(events)-i)
			break
		}
		when := "All day"
		if !event.AllDay {
			when = event.Start.In(s.loc).Format("15:04") + "–" + event.End.In(s.loc).Format("15:04")
		}
		fmt.Fprintf(&body, "%s: %s", when, digestText(event.Summary, 200))
		if event.Location != "" {
			fmt.Fprintf(&body, " (%s)", digestText(event.Location, 200))
		}
		body.WriteByte('\n')
	}
	return strings.TrimSpace(body.String()), nil
}

// Bound every externally supplied field and keep previews on one line.
func digestText(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}
