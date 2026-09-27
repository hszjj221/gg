package cliapp

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/media"
)

const mediaUsage = `usage: gg media <image|tts|stt> [options]

  image "prompt" [--size 1024x1024] [--n 1]   generate image(s), save PNG(s)
  tts "text" [--voice NAME] [--format mp3]    synthesize speech, save audio
  stt <audio-file> [--language zh]            transcribe audio to text

Media endpoints default to the chat provider's base URL / API key; override
with GG_MEDIA_BASE_URL / GG_MEDIA_API_KEY / GG_MEDIA_{IMAGE,TTS,STT}_MODEL.`

// mediaClientForCLI builds the media client from resolved config.
func mediaClientForCLI(cfg config.Config) *media.Client {
	baseURL := cfg.MediaBaseURL
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}
	apiKey := cfg.MediaAPIKey
	if apiKey == "" {
		apiKey = cfg.APIKey
	}
	return media.NewClient(media.Config{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		ImageModel: cfg.MediaImageModel,
		TTSModel:   cfg.MediaTTSModel,
		STTModel:   cfg.MediaSTTModel,
		Dir:        filepath.Join(cfg.HomeDir, ".gg", "media"),
	})
}

func runMediaCommand(ctx context.Context, cfg config.Config, mediaArgs []string, stdout, stderr io.Writer) int {
	if len(mediaArgs) == 0 {
		fmt.Fprintln(stdout, mediaUsage)
		return 2
	}
	client := mediaClientForCLI(cfg)
	switch mediaArgs[0] {
	case "image":
		return runMediaImage(ctx, client, mediaArgs[1:], stdout, stderr)
	case "tts":
		return runMediaTTS(ctx, client, mediaArgs[1:], stdout, stderr)
	case "stt":
		return runMediaSTT(ctx, client, mediaArgs[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, mediaUsage)
		return 2
	}
}

func runMediaImage(ctx context.Context, client *media.Client, args []string, stdout, stderr io.Writer) int {
	var prompt, size string
	n := 1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--size":
			i++
			if i < len(args) {
				size = args[i]
			}
		case "--n":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &n)
			}
		default:
			if prompt == "" {
				prompt = args[i]
			}
		}
	}
	if prompt == "" {
		fmt.Fprintln(stderr, "usage: gg media image \"prompt\" [--size 1024x1024] [--n 1]")
		return 2
	}
	results, err := client.GenerateImage(ctx, media.ImageRequest{Prompt: prompt, Size: size, N: n})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, r := range results {
		fmt.Fprintln(stdout, r.Path)
	}
	return 0
}

func runMediaTTS(ctx context.Context, client *media.Client, args []string, stdout, stderr io.Writer) int {
	var text, voice, format string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--voice":
			i++
			if i < len(args) {
				voice = args[i]
			}
		case "--format":
			i++
			if i < len(args) {
				format = args[i]
			}
		default:
			if text == "" {
				text = args[i]
			}
		}
	}
	if text == "" {
		fmt.Fprintln(stderr, "usage: gg media tts \"text\" [--voice NAME] [--format mp3]")
		return 2
	}
	path, err := client.Speak(ctx, media.SpeakRequest{Text: text, Voice: voice, Format: format})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

func runMediaSTT(ctx context.Context, client *media.Client, args []string, stdout, stderr io.Writer) int {
	var audio, language string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--language":
			i++
			if i < len(args) {
				language = args[i]
			}
		default:
			if audio == "" {
				audio = args[i]
			}
		}
	}
	if audio == "" {
		fmt.Fprintln(stderr, "usage: gg media stt <audio-file> [--language zh]")
		return 2
	}
	text, err := client.Transcribe(ctx, media.TranscribeRequest{AudioPath: audio, Language: language})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, text)
	return 0
}
