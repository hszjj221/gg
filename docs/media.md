# 媒体与语音（docs/media.md）

gg 的媒体能力（图片生成、TTS、STT）走 **OpenAI 兼容的 HTTP 端点**：

| 能力 | 端点 |
|---|---|
| 图片生成 | `POST /v1/images/generations` |
| TTS | `POST /v1/audio/speech` |
| STT | `POST /v1/audio/transcriptions`（multipart） |

默认复用聊天 provider 的 `baseURL` / `apiKey`（大多数 OpenAI 兼容服务三个端点都提供）。需要独立配置时用环境变量：

- `GG_MEDIA_BASE_URL` / `GG_MEDIA_API_KEY`
- `GG_MEDIA_IMAGE_MODEL` / `GG_MEDIA_TTS_MODEL` / `GG_MEDIA_STT_MODEL`

生成物存到 `~/.gg/media/{images,tts}/`（0600），STT 直接返回文本。

## Agent 工具

- `image_generate`：prompt → PNG 文件路径。**需要 approval**（图片生成按次计费）。
- `tts`：文本 → 音频文件路径（默认 mp3）。
- `stt`：本地音频文件 → 转写文本。

## CLI

```bash
gg media image "一只橘猫在看代码" --size 1024x1024
gg media tts "你好，我是叮当" --format mp3
gg media stt ./voice.m4a --language zh
```

## 限制

- STT 音频上限 64 MiB；图片/TTS 响应上限 64 MiB。
- 图片一次最多 4 张。
- 图片生成质量完全取决于 provider（DALL-E / gpt-image / 第三方兼容实现）。
