package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
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

// messagePart is one node of a Gmail payload MIME tree.
type messagePart struct {
	MimeType string `json:"mimeType"`
	Body     struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []messagePart `json:"parts"`
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
			Parts []messagePart `json:"parts"`
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
	// Walk the MIME tree depth-first: real messages often nest text/plain
	// inside multipart/alternative inside multipart/mixed. Prefer text/plain,
	// fall back to the top-level body.
	body := findTextPart(raw.Payload.Parts)
	if body == "" && raw.Payload.Body.Data != "" {
		body = decodeB64(raw.Payload.Body.Data)
	}
	msg.Body = truncateBody(strings.TrimSpace(body))
	return msg, nil
}

// findTextPart returns the decoded text/plain part, searching nested
// multipart children recursively.
func findTextPart(parts []messagePart) string {
	for _, p := range parts {
		if strings.HasPrefix(p.MimeType, "text/plain") && p.Body.Data != "" {
			return decodeB64(p.Body.Data)
		}
		if s := findTextPart(p.Parts); s != "" {
			return s
		}
	}
	return ""
}

// maxBodyRunes bounds what is handed to the model; the API response itself
// is decoded in full (up to maxResponseBytes).
const maxBodyRunes = 12000

func truncateBody(s string) string {
	r := []rune(s)
	if len(r) <= maxBodyRunes {
		return s
	}
	return string(r[:maxBodyRunes]) + "\n\n[正文过长，已截断]"
}

// Send sends a plain-text email.
func (c *Client) Send(ctx context.Context, to, subject, body string) (string, error) {
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(subject, "\r\n") {
		return "", fmt.Errorf("to/subject must not contain line breaks")
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return "", fmt.Errorf("invalid to address: %w", err)
	}
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

// maxResponseBytes bounds a single API response body: Gmail messages can be
// several MiB, so the old 1 MiB cap broke large but valid messages. The body
// handed to the model is truncated separately (truncateBody).
const maxResponseBytes = 10 << 20

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
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(v)
}

func decodeB64(s string) string {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}
