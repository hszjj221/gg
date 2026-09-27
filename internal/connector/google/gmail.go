package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MessageSummary is one row of a Gmail search result.
type MessageSummary struct {
	ID       string
	ThreadID string
}

// Message is a decoded Gmail message (text/plain preferred).
type Message struct {
	ID      string
	From    string
	Subject string
	Date    string
	Body    string
	Snippet string
}

// Search lists message ids matching Gmail search syntax, e.g.
// "newer_than:1d", "from:boss@example.com", "is:unread".
func (c *Client) Search(ctx context.Context, query string, max int) ([]MessageSummary, error) {
	if max <= 0 || max > 100 {
		max = 10
	}
	u := APIBase + "/gmail/v1/users/me/messages?q=" + url.QueryEscape(query) +
		fmt.Sprintf("&maxResults=%d", max)
	resp, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
		Error *apiError `json:"error"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, out.Error
	}
	summaries := make([]MessageSummary, 0, len(out.Messages))
	for _, m := range out.Messages {
		summaries = append(summaries, MessageSummary{ID: m.ID, ThreadID: m.ThreadID})
	}
	return summaries, nil
}

// Read fetches and decodes one message.
func (c *Client) Read(ctx context.Context, id string) (Message, error) {
	u := APIBase + "/gmail/v1/users/me/messages/" + url.PathEscape(id) + "?format=full"
	resp, err := c.Get(ctx, u)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()
	var raw struct {
		ID      string `json:"id"`
		Snippet string `json:"snippet"`
		Payload struct {
			Headers []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"headers"`
			Body struct {
				Data string `json:"data"`
			} `json:"body"`
			Parts []struct {
				MimeType string `json:"mimeType"`
				Body     struct {
					Data string `json:"data"`
				} `json:"body"`
			} `json:"parts"`
		} `json:"payload"`
		Error *apiError `json:"error"`
	}
	if err := decodeJSON(resp, &raw); err != nil {
		return Message{}, err
	}
	if raw.Error != nil {
		return Message{}, raw.Error
	}
	msg := Message{ID: raw.ID, Snippet: raw.Snippet}
	for _, h := range raw.Payload.Headers {
		switch strings.ToLower(h.Name) {
		case "from":
			msg.From = h.Value
		case "subject":
			msg.Subject = h.Value
		case "date":
			msg.Date = h.Value
		}
	}
	// Prefer the first text/plain part, fall back to the top-level body.
	body := ""
	for _, p := range raw.Payload.Parts {
		if strings.HasPrefix(p.MimeType, "text/plain") && p.Body.Data != "" {
			body = decodeB64(p.Body.Data)
			break
		}
	}
	if body == "" && raw.Payload.Body.Data != "" {
		body = decodeB64(raw.Payload.Body.Data)
	}
	msg.Body = strings.TrimSpace(body)
	return msg, nil
}

// Send sends a plain-text email.
func (c *Client) Send(ctx context.Context, to, subject, body string) (string, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "To: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", to, subject, body)
	rawMsg := base64.RawURLEncoding.EncodeToString(buf.Bytes())
	payload, _ := json.Marshal(map[string]string{"raw": rawMsg})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		APIBase+"/gmail/v1/users/me/messages/send", bytes.NewReader(payload))
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

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return fmt.Sprintf("google api: %d %s", e.Code, e.Message) }

func decodeJSON(resp *http.Response, v any) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var wrapped struct {
			Error *apiError `json:"error"`
		}
		if json.Unmarshal(body, &wrapped) == nil && wrapped.Error != nil {
			return wrapped.Error
		}
		return fmt.Errorf("google api: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

func decodeB64(s string) string {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}
