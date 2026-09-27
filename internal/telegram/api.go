// Package telegram implements the Telegram Bot channel: long-polling
// getUpdates, per-chat agent sessions, and a voice pipeline
// (voice message -> STT -> agent -> text reply + TTS voice reply).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// API is a minimal Telegram Bot API client (stdlib only).
type API struct {
	token   string
	baseURL string
	client  *http.Client
}

// NewAPI builds a client for the given bot token. The token never leaves
// this package except in HTTPS request URLs/paths.
func NewAPI(token string) *API {
	return newAPIForURL(token, "https://api.telegram.org")
}

func newAPIForURL(token, baseURL string) *API {
	return &API{
		token:   token,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  &http.Client{Timeout: 90 * time.Second},
	}
}

func (a *API) methodURL(method string) string {
	return a.baseURL + "/bot" + a.token + "/" + method
}

func (a *API) fileURL(filePath string) string {
	return a.baseURL + "/file/bot" + a.token + "/" + filePath
}

// Update is the subset of a Telegram update we care about.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from"`
	Text      string `json:"text"`
	Voice     *Voice `json:"voice"`
	Audio     *Audio `json:"audio"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Voice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
}

type Audio struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (a *API) call(ctx context.Context, method string, params map[string]string) (json.RawMessage, error) {
	form := make([]byte, 0)
	_ = form
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range params {
		_ = w.WriteField(k, v)
	}
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.methodURL(method), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("telegram %s: bad response: %w", method, err)
	}
	if !ar.OK {
		if ar.ErrorCode == 429 && ar.Parameters != nil && ar.Parameters.RetryAfter > 0 {
			return nil, &RateLimitedError{RetryAfter: time.Duration(ar.Parameters.RetryAfter) * time.Second}
		}
		return nil, fmt.Errorf("telegram %s: %d %s", method, ar.ErrorCode, ar.Description)
	}
	return ar.Result, nil
}

// RateLimitedError is returned when the Bot API answers 429.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("telegram rate limited, retry after %s", e.RetryAfter)
}

// GetUpdates long-polls for updates. timeout is the server-side long-poll
// window in seconds (0-50).
func (a *API) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	params := map[string]string{
		"offset":  fmt.Sprintf("%d", offset),
		"timeout": fmt.Sprintf("%d", timeout),
	}
	raw, err := a.call(ctx, "getUpdates", params)
	if err != nil {
		return nil, err
	}
	var updates []Update
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, fmt.Errorf("telegram getUpdates: decode: %w", err)
	}
	return updates, nil
}

// SendMessage sends a text message, splitting it into 4096-char chunks.
func (a *API) SendMessage(ctx context.Context, chatID int64, text string) error {
	for _, chunk := range splitMessage(text, 4096) {
		if _, err := a.call(ctx, "sendMessage", map[string]string{
			"chat_id": fmt.Sprintf("%d", chatID),
			"text":    chunk,
		}); err != nil {
			return err
		}
	}
	return nil
}

// SendVoice sends an audio file as a voice message.
func (a *API) SendVoice(ctx context.Context, chatID int64, audioPath, caption string) error {
	f, err := os.Open(audioPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	part, err := w.CreateFormFile("voice", filepath.Base(audioPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, io.LimitReader(f, 50<<20)); err != nil {
		return err
	}
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.methodURL("sendVoice"), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram sendVoice: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil || !ar.OK {
		return fmt.Errorf("telegram sendVoice failed: %s", ar.Description)
	}
	return nil
}

// GetFilePath resolves a file_id to its downloadable path.
func (a *API) GetFilePath(ctx context.Context, fileID string) (string, error) {
	raw, err := a.call(ctx, "getFile", map[string]string{"file_id": fileID})
	if err != nil {
		return "", err
	}
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", err
	}
	if f.FilePath == "" {
		return "", fmt.Errorf("telegram getFile: empty file_path")
	}
	return f.FilePath, nil
}

// DownloadFile fetches https://api.telegram.org/file/bot<token>/<filePath>
// into dst (capped at 25 MiB).
func (a *API) DownloadFile(ctx context.Context, filePath, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.fileURL(filePath), nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram download: HTTP %d", resp.StatusCode)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	const maxBytes = 25 << 20
	n, err := io.Copy(out, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		os.Remove(dst)
		return err
	}
	if n > maxBytes {
		os.Remove(dst)
		return fmt.Errorf("telegram download: file exceeds 25 MiB")
	}
	return nil
}

// GetMe returns the bot's username (used once at startup to verify the token).
func (a *API) GetMe(ctx context.Context) (string, error) {
	raw, err := a.call(ctx, "getMe", nil)
	if err != nil {
		return "", err
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

func splitMessage(s string, limit int) []string {
	if len(s) <= limit {
		return []string{s}
	}
	var out []string
	runes := []rune(s)
	for len(runes) > limit {
		out = append(out, string(runes[:limit]))
		runes = runes[limit:]
	}
	out = append(out, string(runes))
	return out
}
