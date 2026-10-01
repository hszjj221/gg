// Package media implements image generation, text-to-speech and
// speech-to-text against OpenAI-compatible HTTP endpoints
// (/v1/images/generations, /v1/audio/speech, /v1/audio/transcriptions).
// It is provider-agnostic: any base URL + API key that speaks the
// OpenAI REST dialect works.
package media

import (
	"bytes"
	"context"
	"encoding/base64"
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

// Config selects the media backend. Empty fields fall back to the chat
// provider's base URL / API key (most OpenAI-compatible providers serve
// the media endpoints too).
type Config struct {
	BaseURL string
	APIKey  string
	// ImageModel, TTSModel, STTModel optionally pin per-capability models.
	ImageModel string
	TTSModel   string
	STTModel   string
	// HTTPClient may be overridden in tests.
	HTTPClient *http.Client
	// Dir is where generated media is stored; defaults to ~/.gg/media.
	Dir string
}

// Client talks to the media endpoints.
type Client struct {
	cfg Config
	hc  *http.Client
}

func NewClient(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	return &Client{cfg: cfg, hc: hc}
}

func (c *Client) baseURL() string {
	return strings.TrimRight(c.cfg.BaseURL, "/")
}

func (c *Client) mediaDir(kind string) (string, error) {
	dir := c.cfg.Dir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".gg", "media")
	}
	d := filepath.Join(dir, kind)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

func stampName(ext string) string {
	return time.Now().UTC().Format("20060102-150405.000000") + ext
}

// ImageRequest describes one image generation.
type ImageRequest struct {
	Prompt string
	// Size like "1024x1024". Empty means provider default.
	Size string
	// N is the number of images; most providers cap at 1-4.
	N int
}

// ImageResult is one generated image saved to disk.
type ImageResult struct {
	Path string
	// RevisedPrompt is what the provider actually rendered (may be empty).
	RevisedPrompt string
}

// GenerateImage calls /v1/images/generations and saves each image to
// ~/.gg/media/images/. Prefers b64_json; falls back to downloading url.
func (c *Client) GenerateImage(ctx context.Context, req ImageRequest) ([]ImageResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("media: image prompt is empty")
	}
	n := req.N
	if n <= 0 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	payload := map[string]any{
		"prompt":          req.Prompt,
		"n":               n,
		"response_format": "b64_json",
	}
	if req.Size != "" {
		payload["size"] = req.Size
	}
	if c.cfg.ImageModel != "" {
		payload["model"] = c.cfg.ImageModel
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("media: image request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, c.apiError(resp, "image")
	}
	var out struct {
		Data []struct {
			B64     string `json:"b64_json"`
			URL     string `json:"url"`
			Revised string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("media: decode image response: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("media: image response had no data")
	}
	dir, err := c.mediaDir("images")
	if err != nil {
		return nil, err
	}
	var results []ImageResult
	for _, d := range out.Data {
		var raw []byte
		switch {
		case d.B64 != "":
			raw, err = base64.StdEncoding.DecodeString(d.B64)
			if err != nil {
				return nil, fmt.Errorf("media: decode image bytes: %w", err)
			}
		case d.URL != "":
			raw, err = c.download(ctx, d.URL)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("media: image entry had neither b64_json nor url")
		}
		path := filepath.Join(dir, stampName(".png"))
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return nil, err
		}
		results = append(results, ImageResult{Path: path, RevisedPrompt: d.Revised})
	}
	return results, nil
}

func (c *Client) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("media: download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("media: download image: status %d", resp.StatusCode)
	}
	return readCappedBody(resp, 64<<20, "image download")
}

// readCappedBody reads at most limit bytes; it errors instead of silently
// truncating when the body is larger.
func readCappedBody(resp *http.Response, limit int64, op string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("media: read %s response: %w", op, err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("media: %s response exceeds %d bytes", op, limit)
	}
	return raw, nil
}

// DefaultVoice is the provider voice used when SpeakRequest.Voice is empty.
// It is a named constant (rather than a literal in Speak) so approval
// prompts and API calls describe the same effective voice.
const DefaultVoice = "alloy"

// SpeakRequest describes one TTS synthesis.
type SpeakRequest struct {
	Text string
	// Voice selects the provider voice; empty means DefaultVoice.
	Voice string
	// Format is the audio container, e.g. "mp3". Empty means mp3.
	Format string
}

// Speak calls /v1/audio/speech and saves the MP3 to ~/.gg/media/tts/.
func (c *Client) Speak(ctx context.Context, req SpeakRequest) (string, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return "", fmt.Errorf("media: tts text is empty")
	}
	format := req.Format
	if format == "" {
		format = "mp3"
	}
	model := c.cfg.TTSModel
	if model == "" {
		model = "tts-1"
	}
	voice := req.Voice
	if voice == "" {
		voice = DefaultVoice
	}
	payload := map[string]any{
		"input":           text,
		"response_format": format,
		"model":           model,
		"voice":           voice,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("media: tts request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", c.apiError(resp, "tts")
	}
	raw, err := readCappedBody(resp, 64<<20, "tts")
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("media: tts returned empty audio")
	}
	dir, err := c.mediaDir("tts")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, stampName("."+format))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// TranscribeRequest describes one STT transcription.
type TranscribeRequest struct {
	// AudioPath is a local audio file (mp3/wav/m4a/ogg...).
	AudioPath string
	// Language is a BCP-47 hint, e.g. "zh". Empty means auto-detect.
	Language string
}

// Transcribe calls /v1/audio/transcriptions (multipart) and returns text.
func (c *Client) Transcribe(ctx context.Context, req TranscribeRequest) (string, error) {
	f, err := os.Open(req.AudioPath)
	if err != nil {
		return "", fmt.Errorf("media: open audio: %w", err)
	}
	defer f.Close()
	// Cap the copy itself: Stat size is untrusted (FIFOs/devices lie or grow).
	const maxAudioBytes = 64 << 20
	limited := io.LimitReader(f, maxAudioBytes+1)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	model := c.cfg.STTModel
	if model == "" {
		model = "whisper-1"
	}
	_ = w.WriteField("model", model)
	if req.Language != "" {
		_ = w.WriteField("language", req.Language)
	}
	_ = w.WriteField("response_format", "json")
	part, err := w.CreateFormFile("file", filepath.Base(req.AudioPath))
	if err != nil {
		return "", err
	}
	n, err := io.Copy(part, limited)
	if err != nil {
		return "", fmt.Errorf("media: read audio: %w", err)
	}
	if n > maxAudioBytes {
		return "", fmt.Errorf("media: audio file too large (max 64 MiB)")
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", w.FormDataContentType())
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("media: stt request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", c.apiError(resp, "stt")
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("media: decode stt response: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

func (c *Client) apiError(resp *http.Response, op string) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &parsed) == nil && parsed.Error.Message != "" {
		msg = parsed.Error.Message
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return fmt.Errorf("media: %s failed (status %d): %s", op, resp.StatusCode, msg)
}
