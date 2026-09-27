package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Event is a calendar event summary.
type Event struct {
	ID          string
	Summary     string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	AllDay      bool
}

// Agenda lists events on the primary calendar in [from, from+days).
func (c *Client) Agenda(ctx context.Context, from time.Time, days int) ([]Event, error) {
	if days <= 0 || days > 30 {
		days = 1
	}
	to := from.AddDate(0, 0, days)
	q := url.Values{}
	q.Set("timeMin", from.Format(time.RFC3339))
	q.Set("timeMax", to.Format(time.RFC3339))
	q.Set("singleEvents", "true")
	q.Set("orderBy", "startTime")
	q.Set("maxResults", "50")
	u := CalBase + "/calendar/v3/calendars/primary/events?" + q.Encode()
	resp, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Items []struct {
			ID          string `json:"id"`
			Summary     string `json:"summary"`
			Description string `json:"description"`
			Location    string `json:"location"`
			Start       struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"end"`
		} `json:"items"`
		Error *apiError `json:"error"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, out.Error
	}
	events := make([]Event, 0, len(out.Items))
	for _, it := range out.Items {
		ev := Event{
			ID:          it.ID,
			Summary:     it.Summary,
			Description: it.Description,
			Location:    it.Location,
		}
		if it.Start.DateTime != "" {
			ev.Start, _ = time.Parse(time.RFC3339, it.Start.DateTime)
			ev.End, _ = time.Parse(time.RFC3339, it.End.DateTime)
		} else {
			ev.AllDay = true
			ev.Start, _ = time.Parse("2006-01-02", it.Start.Date)
			ev.End, _ = time.Parse("2006-01-02", it.End.Date)
		}
		events = append(events, ev)
	}
	return events, nil
}

// CreateEvent inserts an event on the primary calendar.
func (c *Client) CreateEvent(ctx context.Context, summary string, start, end time.Time, description, location string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"summary":     summary,
		"description": description,
		"location":    location,
		"start":       map[string]string{"dateTime": start.Format(time.RFC3339)},
		"end":         map[string]string{"dateTime": end.Format(time.RFC3339)},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		CalBase+"/calendar/v3/calendars/primary/events", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ID    string    `json:"id"`
		Error *apiError `json:"error"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", out.Error
	}
	return out.ID, nil
}

// ParseDateTime accepts "2006-01-02 15:04" (in loc) or RFC3339.
func ParseDateTime(s string, loc *time.Location) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, loc); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid datetime %q (want \"2006-01-02 15:04\" or RFC3339)", s)
}
