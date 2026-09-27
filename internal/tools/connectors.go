package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/connector/google"
)

// GmailSearchTool lets the agent search the user's Gmail.
type GmailSearchTool struct{ client *google.Client }

func NewGmailSearchTool(client *google.Client) GmailSearchTool { return GmailSearchTool{client} }

func (t GmailSearchTool) Name() string { return "gmail_search" }

func (t GmailSearchTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "gmail_search",
		Description: "Search the user's Gmail. Query uses Gmail search syntax, e.g. \"newer_than:1d\" (today's mail), \"from:boss@example.com\", \"is:unread\". Returns message ids; use gmail_read to read one.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "description": "Gmail search query."},
				"max_results": map[string]any{"type": "integer", "description": "Max results (default 10, max 100)."},
			},
			"required": []string{"query"},
		},
	}
}

func (t GmailSearchTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid gmail_search arguments: %w", err))
	}
	msgs, err := t.client.Search(ctx, input.Query, input.MaxResults)
	if err != nil {
		return errorResult(err)
	}
	if len(msgs) == 0 {
		return textResult("no messages found")
	}
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "- %s\n", m.ID)
	}
	return textResult(strings.TrimSpace(b.String()))
}

// GmailReadTool lets the agent read one Gmail message.
type GmailReadTool struct{ client *google.Client }

func NewGmailReadTool(client *google.Client) GmailReadTool { return GmailReadTool{client} }

func (t GmailReadTool) Name() string { return "gmail_read" }

func (t GmailReadTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "gmail_read",
		Description: "Read one Gmail message by id (from gmail_search). Returns From/Subject/Date and the plain-text body.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Gmail message id."},
			},
			"required": []string{"id"},
		},
	}
}

func (t GmailReadTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid gmail_read arguments: %w", err))
	}
	msg, err := t.client.Read(ctx, input.ID)
	if err != nil {
		return errorResult(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\nSubject: %s\nDate: %s\n\n%s", msg.From, msg.Subject, msg.Date, msg.Body)
	return textResult(b.String())
}

// GmailSendTool lets the agent send an email. Requires approval.
type GmailSendTool struct{ client *google.Client }

func NewGmailSendTool(client *google.Client) GmailSendTool { return GmailSendTool{client} }

func (t GmailSendTool) Name() string { return "gmail_send" }

func (t GmailSendTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "gmail_send",
		Description: "Send a plain-text email from the user's Gmail account. Requires user approval.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":      map[string]any{"type": "string", "description": "Recipient email address."},
				"subject": map[string]any{"type": "string", "description": "Email subject."},
				"body":    map[string]any{"type": "string", "description": "Plain-text body."},
			},
			"required": []string{"to", "subject", "body"},
		},
	}
}

type gmailSendInput struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func (t GmailSendTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input gmailSendInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid gmail_send arguments: %w", err)
	}
	return agent.ApprovalRequest{
		ToolName:  "gmail_send",
		Summary:   fmt.Sprintf("send email to %s: %q", input.To, input.Subject),
		Details:   fmt.Sprintf("to: %s\nsubject: %s\n\n%s", input.To, input.Subject, truncateRunes(input.Body, 1000)),
		Arguments: raw,
	}, nil
}

func (t GmailSendTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input gmailSendInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid gmail_send arguments: %w", err))
	}
	if input.To == "" || input.Subject == "" {
		return errorResult(fmt.Errorf("to and subject are required"))
	}
	id, err := t.client.Send(ctx, input.To, input.Subject, input.Body)
	if err != nil {
		return errorResult(err)
	}
	return textResult("sent (id " + id + ")")
}

// CalendarAgendaTool lists upcoming events on the primary calendar.
type CalendarAgendaTool struct {
	client *google.Client
	loc    *time.Location
	// tzErr is set when the profile timezone is configured but invalid: the
	// tool refuses to run rather than silently interpreting wall-clock input
	// in the wrong zone.
	tzErr error
}

func NewCalendarAgendaTool(client *google.Client, loc *time.Location, tzErr error) CalendarAgendaTool {
	if loc == nil {
		loc = time.Local
	}
	return CalendarAgendaTool{client: client, loc: loc, tzErr: tzErr}
}

func (t CalendarAgendaTool) Name() string { return "calendar_agenda" }

func (t CalendarAgendaTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "calendar_agenda",
		Description: "List events on the user's primary Google Calendar for the next N days (default 1, max 30).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"days": map[string]any{"type": "integer", "minimum": 1, "maximum": 30, "description": "Number of days from today (default 1, max 30)."},
			},
		},
	}
}

func (t CalendarAgendaTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	if t.tzErr != nil {
		return errorResult(t.tzErr)
	}
	var input struct {
		Days int `json:"days"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid calendar_agenda arguments: %w", err))
	}
	now := time.Now().In(t.loc)
	events, err := t.client.Agenda(ctx, now, input.Days)
	if err != nil {
		return errorResult(err)
	}
	if len(events) == 0 {
		return textResult("no events")
	}
	var b strings.Builder
	for _, e := range events {
		start := e.Start.In(t.loc)
		end := e.End.In(t.loc)
		if e.AllDay {
			// All-day events need their date: the agenda can span days.
			// The date is date-only (parsed as midnight UTC); format in UTC
			// so timezones west of UTC don't show the previous day.
			fmt.Fprintf(&b, "- %s \u2014 %s (all day)\n", e.Summary, start.UTC().Format("2006-01-02"))
		} else if start.Format("2006-01-02") == end.Format("2006-01-02") {
			fmt.Fprintf(&b, "- %s \u2014 %s to %s\n",
				e.Summary, start.Format("01-02 15:04"), end.Format("15:04"))
		} else {
			// Overnight: show the end date so it doesn't read as ending
			// before it starts.
			fmt.Fprintf(&b, "- %s \u2014 %s to %s\n",
				e.Summary, start.Format("01-02 15:04"), end.Format("01-02 15:04"))
		}
	}
	return textResult(strings.TrimSpace(b.String()))
}

// CalendarCreateTool creates a calendar event. Requires approval.
type CalendarCreateTool struct {
	client *google.Client
	loc    *time.Location
	tzErr  error
}

func NewCalendarCreateTool(client *google.Client, loc *time.Location, tzErr error) CalendarCreateTool {
	if loc == nil {
		loc = time.Local
	}
	return CalendarCreateTool{client: client, loc: loc, tzErr: tzErr}
}

func (t CalendarCreateTool) Name() string { return "calendar_create" }

func (t CalendarCreateTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "calendar_create",
		Description: "Create an event on the user's primary Google Calendar. Times are \"2006-01-02 15:04\" in the user's timezone or RFC3339. Requires user approval.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       map[string]any{"type": "string", "description": "Event title."},
				"start":       map[string]any{"type": "string", "description": "Start time."},
				"end":         map[string]any{"type": "string", "description": "End time."},
				"description": map[string]any{"type": "string", "description": "Optional description."},
				"location":    map[string]any{"type": "string", "description": "Optional location."},
			},
			"required": []string{"title", "start", "end"},
		},
	}
}

type calendarCreateInput struct {
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Description string `json:"description"`
	Location    string `json:"location"`
}

func (t CalendarCreateTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input calendarCreateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid calendar_create arguments: %w", err)
	}
	return agent.ApprovalRequest{
		ToolName:  "calendar_create",
		Summary:   fmt.Sprintf("create calendar event %q (%s – %s)", input.Title, input.Start, input.End),
		Details:   fmt.Sprintf("title: %s\nstart: %s\nend: %s\nlocation: %s\ndescription: %s", input.Title, input.Start, input.End, input.Location, input.Description),
		Arguments: raw,
	}, nil
}

func (t CalendarCreateTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	// A bad profile timezone must not silently fall back to time.Local and
	// create the event at the wrong instant.
	if t.tzErr != nil {
		return errorResult(t.tzErr)
	}
	var input calendarCreateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid calendar_create arguments: %w", err))
	}
	start, err := google.ParseDateTime(input.Start, t.loc)
	if err != nil {
		return errorResult(err)
	}
	end, err := google.ParseDateTime(input.End, t.loc)
	if err != nil {
		return errorResult(err)
	}
	if !end.After(start) {
		return errorResult(fmt.Errorf("end must be after start"))
	}
	id, err := t.client.CreateEvent(ctx, input.Title, start, end, input.Description, input.Location)
	if err != nil {
		return errorResult(err)
	}
	return textResult("created event (id " + id + ")")
}

// truncateRunes shortens s to n runes for previews.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
