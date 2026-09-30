# Media & voice (docs/media.md)

gg's media capabilities (image generation, TTS, STT) go through **OpenAI-compatible HTTP endpoints**:

| Capability | Endpoint |
|---|---|
| Image generation | `POST /v1/images/generations` |
| TTS | `POST /v1/audio/speech` |
| STT | `POST /v1/audio/transcriptions` (multipart) |

By default the chat provider's `baseURL` / `apiKey` are reused (most OpenAI-compatible services offer all three endpoints). Use environment variables for separate configuration:

- `GG_MEDIA_BASE_URL` / `GG_MEDIA_API_KEY`
- `GG_MEDIA_IMAGE_MODEL` / `GG_MEDIA_TTS_MODEL` / `GG_MEDIA_STT_MODEL`

Generated files go to `~/.gg/media/{images,tts}/` (0600); STT returns text directly.

## Agent tools

- `image_generate`: prompt → PNG file path. **Requires approval** (image generation is billed per call).
- `tts`: text → audio file path (mp3 by default).
- `stt`: local audio file → transcript text.

## CLI

```bash
gg media image "一只橘猫在看代码" --size 1024x1024
gg media tts "你好，我是叮当" --format mp3
gg media stt ./voice.m4a --language zh
```

## Limits

- STT audio capped at 64 MiB; image/TTS responses capped at 64 MiB.
- At most 4 images per request.
- Image quality depends entirely on the provider (DALL-E / gpt-image / third-party compatible implementations).
