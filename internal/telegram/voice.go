package telegram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hszjj221/gg/internal/media"
)

// handleVoice implements the voice pipeline: download the voice message,
// transcribe it with STT, run the agent, then reply with text and a TTS
// voice message.
func (b *Bot) handleVoice(ctx context.Context, msg *Message) {
	if b.mclient == nil {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "语音功能未配置（缺少媒体服务）。请直接发文字。")
		return
	}
	var fileID string
	switch {
	case msg.Voice != nil:
		fileID = msg.Voice.FileID
	case msg.Audio != nil:
		fileID = msg.Audio.FileID
	}

	chatDir := filepath.Join(b.mediaDir, fmt.Sprintf("chat-%d", msg.Chat.ID))
	if err := os.MkdirAll(chatDir, 0o700); err != nil {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "出错了："+err.Error())
		return
	}
	audioPath := filepath.Join(chatDir, fmt.Sprintf("in-%d.ogg", time.Now().UnixNano()))

	filePath, err := b.api.GetFilePath(ctx, fileID)
	if err != nil {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "下载语音失败："+err.Error())
		return
	}
	if err := b.api.DownloadFile(ctx, filePath, audioPath); err != nil {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "下载语音失败："+err.Error())
		return
	}
	defer os.Remove(audioPath)

	text, err := b.mclient.Transcribe(ctx, media.TranscribeRequest{AudioPath: audioPath})
	if err != nil {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "语音转文字失败："+err.Error())
		return
	}
	if text == "" {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "没听清，请再说一遍。")
		return
	}

	reply, err := b.runAgent(ctx, msg.Chat.ID, text)
	if err != nil {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "出错了："+err.Error())
		return
	}
	if reply == "" {
		reply = "（空回复）"
	}
	// Text reply first so the user always gets something even if TTS fails.
	_ = b.api.SendMessage(ctx, msg.Chat.ID, reply)

	voicePath, err := b.mclient.Speak(ctx, media.SpeakRequest{Text: reply, Format: "opus"})
	if err != nil {
		// Text already delivered; TTS failure is non-fatal.
		return
	}
	defer os.Remove(voicePath)
	_ = b.api.SendVoice(ctx, msg.Chat.ID, voicePath, "")
}
