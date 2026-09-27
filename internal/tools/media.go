package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/media"
)

// ImageGenerateTool generates images from a text prompt. Requires approval:
// image generation costs money on most providers.
type ImageGenerateTool struct {
	client *media.Client
}

func NewImageGenerateTool(client *media.Client) ImageGenerateTool {
	return ImageGenerateTool{client: client}
}

func (t ImageGenerateTool) Name() string { return "image_generate" }

func (t ImageGenerateTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "image_generate",
		Description: "Generate image(s) from a text prompt and save as PNG file(s). Returns the file path(s). Requires user approval.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "Detailed image description."},
				"size":   map[string]any{"type": "string", "description": "Optional size like \"1024x1024\"."},
				"n":      map[string]any{"type": "integer", "description": "Number of images (1-4, default 1)."},
			},
			"required": []string{"prompt"},
		},
	}
}

type imageGenerateInput struct {
	Prompt string `json:"prompt"`
	Size   string `json:"size"`
	N      int    `json:"n"`
}

func (t ImageGenerateTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input imageGenerateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid image_generate arguments: %w", err)
	}
	n := input.N
	if n <= 0 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	size := input.Size
	if size == "" {
		size = "default"
	}
	return agent.ApprovalRequest{
		ToolName:  "image_generate",
		Summary:   fmt.Sprintf("generate %d image(s) [%s]: %q", n, size, truncateRunes(input.Prompt, 80)),
		Details:   fmt.Sprintf("count: %d\nsize: %s\nprompt: %s", n, size, input.Prompt),
		Arguments: raw,
	}, nil
}

func (t ImageGenerateTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input imageGenerateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid image_generate arguments: %w", err))
	}
	results, err := t.client.GenerateImage(ctx, media.ImageRequest{
		Prompt: input.Prompt, Size: input.Size, N: input.N,
	})
	if err != nil {
		return errorResult(err)
	}
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "saved: %s\n", r.Path)
		if r.RevisedPrompt != "" {
			fmt.Fprintf(&b, "revised prompt: %s\n", r.RevisedPrompt)
		}
	}
	return textResult(strings.TrimSpace(b.String()))
}

// TTSTool synthesizes speech from text and saves an audio file.
type TTSTool struct {
	client *media.Client
}

func NewTTSTool(client *media.Client) TTSTool { return TTSTool{client: client} }

func (t TTSTool) Name() string { return "tts" }

func (t TTSTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "tts",
		Description: "Convert text to speech and save as an audio file. Returns the file path.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":   map[string]any{"type": "string", "description": "Text to speak."},
				"voice":  map[string]any{"type": "string", "description": "Optional voice name."},
				"format": map[string]any{"type": "string", "description": "Audio format: mp3 (default), wav, ogg."},
			},
			"required": []string{"text"},
		},
	}
}

func (t TTSTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Text   string `json:"text"`
		Voice  string `json:"voice"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid tts arguments: %w", err))
	}
	path, err := t.client.Speak(ctx, media.SpeakRequest{
		Text: input.Text, Voice: input.Voice, Format: input.Format,
	})
	if err != nil {
		return errorResult(err)
	}
	return textResult("saved: " + path)
}

// STTTool transcribes a local audio file to text.
func NewSTTTool(client *media.Client) STTTool { return STTTool{client: client} }

// NewSTTToolWithRoots restricts audio_path to the given roots (symlinks
// resolved), so a prompt cannot exfiltrate arbitrary files.
func NewSTTToolWithRoots(client *media.Client, roots []string) STTTool {
	return STTTool{client: client, roots: roots}
}

type STTTool struct {
	client *media.Client
	roots  []string
}

func (t STTTool) Name() string { return "stt" }

func (t STTTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "stt",
		Description: "Transcribe a local audio file (voice message, recording) to text.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"audio_path": map[string]any{"type": "string", "description": "Path to the audio file."},
				"language":   map[string]any{"type": "string", "description": "Optional BCP-47 language hint, e.g. \"zh\"."},
			},
			"required": []string{"audio_path"},
		},
	}
}

func (t STTTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		AudioPath string `json:"audio_path"`
		Language  string `json:"language"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid stt arguments: %w", err))
	}
	audioPath, err := t.resolveAudioPath(input.AudioPath)
	if err != nil {
		return errorResult(err)
	}
	text, err := t.client.Transcribe(ctx, media.TranscribeRequest{
		AudioPath: audioPath, Language: input.Language,
	})
	if err != nil {
		return errorResult(err)
	}
	return textResult(text)
}

// resolveAudioPath confines audio_path to the configured roots, resolving
// symlinks so a link escaping the root is rejected.
func (t STTTool) resolveAudioPath(path string) (string, error) {
	if len(t.roots) == 0 {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("audio file not accessible: %w", err)
		}
		return path, nil
	}
	var firstErr error
	for _, root := range t.roots {
		target, err := resolveExistingInsideRoot(root, path, "allowed audio root")
		if err == nil {
			return target, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}
