package handler

import (
	"context"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"github.com/nickalie/nclaw/internal/config"
	"github.com/nickalie/nclaw/internal/telegram"
)

func TestWithReplyContext_NoReply(t *testing.T) {
	msg := &models.Message{Text: "hello"}
	result := withReplyContext(msg, "hello")
	assert.Equal(t, "hello", result)
}

func TestWithReplyContext_WithReplyText(t *testing.T) {
	msg := &models.Message{
		Text:           "my reply",
		ReplyToMessage: &models.Message{Text: "original"},
	}
	result := withReplyContext(msg, "my reply")
	assert.Equal(t, "[Replying to message: original]\n\nmy reply", result)
}

func TestWithReplyContext_WithReplyCaption(t *testing.T) {
	msg := &models.Message{
		Text:           "my reply",
		ReplyToMessage: &models.Message{Caption: "photo caption"},
	}
	result := withReplyContext(msg, "my reply")
	assert.Equal(t, "[Replying to message: photo caption]\n\nmy reply", result)
}

func TestWithReplyContext_EmptyOriginal(t *testing.T) {
	msg := &models.Message{
		Text:           "my reply",
		ReplyToMessage: &models.Message{},
	}
	result := withReplyContext(msg, "my reply")
	assert.Equal(t, "my reply", result)
}

func TestMessageContent_TextOnly(t *testing.T) {
	msg := &models.Message{Text: "hello"}
	text, att := messageContent(msg)
	assert.Equal(t, "hello", text)
	assert.Nil(t, att)
}

func TestMessageContent_DocumentWithCaption(t *testing.T) {
	msg := &models.Message{
		Caption:  "file caption",
		Document: &models.Document{FileID: "f1", FileName: "test.pdf"},
	}
	text, att := messageContent(msg)
	assert.Equal(t, "file caption", text)
	assert.NotNil(t, att)
	assert.Equal(t, "f1", att.fileID)
	assert.Equal(t, "test.pdf", att.filename)
}

func TestMessageContent_PhotoNoCaption(t *testing.T) {
	msg := &models.Message{
		Photo: []models.PhotoSize{
			{FileID: "small", Width: 100, Height: 100},
			{FileID: "large", Width: 800, Height: 800},
		},
	}
	text, att := messageContent(msg)
	assert.Empty(t, text)
	assert.NotNil(t, att)
	assert.Equal(t, "large", att.fileID)
	assert.Equal(t, "photo.jpg", att.filename)
}

func TestExtractAttachment_Document(t *testing.T) {
	msg := &models.Message{
		Document: &models.Document{FileID: "doc1", FileName: "report.pdf"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "doc1", att.fileID)
	assert.Equal(t, "report.pdf", att.filename)
}

func TestExtractAttachment_Audio(t *testing.T) {
	msg := &models.Message{
		Audio: &models.Audio{FileID: "a1", FileName: "song.mp3"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "a1", att.fileID)
	assert.Equal(t, "song.mp3", att.filename)
}

func TestExtractAttachment_AudioFallbackName(t *testing.T) {
	msg := &models.Message{
		Audio: &models.Audio{FileID: "a1"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "audio.ogg", att.filename)
}

func TestExtractAttachment_Voice(t *testing.T) {
	msg := &models.Message{
		Voice: &models.Voice{FileID: "v1"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "v1", att.fileID)
	assert.Equal(t, "voice.ogg", att.filename)
}

func TestExtractAttachment_Video(t *testing.T) {
	msg := &models.Message{
		Video: &models.Video{FileID: "vid1", FileName: "clip.mp4"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "vid1", att.fileID)
	assert.Equal(t, "clip.mp4", att.filename)
}

func TestExtractAttachment_VideoFallback(t *testing.T) {
	msg := &models.Message{
		Video: &models.Video{FileID: "vid1"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "video.mp4", att.filename)
}

func TestExtractAttachment_VideoNote(t *testing.T) {
	msg := &models.Message{
		VideoNote: &models.VideoNote{FileID: "vn1"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "vn1", att.fileID)
	assert.Equal(t, "video_note.mp4", att.filename)
}

func TestExtractAttachment_Animation(t *testing.T) {
	msg := &models.Message{
		Animation: &models.Animation{FileID: "an1", FileName: "funny.gif"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "an1", att.fileID)
	assert.Equal(t, "funny.gif", att.filename)
}

func TestExtractAttachment_AnimationFallback(t *testing.T) {
	msg := &models.Message{
		Animation: &models.Animation{FileID: "an1"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "animation.mp4", att.filename)
}

func TestExtractAttachment_Sticker(t *testing.T) {
	msg := &models.Message{
		Sticker: &models.Sticker{FileID: "st1"},
	}
	att := extractAttachment(msg)
	assert.NotNil(t, att)
	assert.Equal(t, "st1", att.fileID)
	assert.Equal(t, "sticker.webp", att.filename)
}

func TestExtractAttachment_None(t *testing.T) {
	msg := &models.Message{Text: "just text"}
	att := extractAttachment(msg)
	assert.Nil(t, att)
}

func TestNameOr(t *testing.T) {
	assert.Equal(t, "given.mp3", nameOr("given.mp3", "fallback.ogg"))
	assert.Equal(t, "fallback.ogg", nameOr("", "fallback.ogg"))
}

func TestIsChatAllowed(t *testing.T) {
	viper.Set("telegram.whitelist_chat_ids", "100,200,300")
	defer viper.Reset()

	assert.True(t, isChatAllowed(100))
	assert.True(t, isChatAllowed(200))
	assert.True(t, isChatAllowed(300))
	assert.False(t, isChatAllowed(999))
}

func TestIsChatAllowed_NoWhitelist(t *testing.T) {
	viper.Reset()

	assert.True(t, isChatAllowed(100))
	assert.True(t, isChatAllowed(999))
}

func TestChatDir_NoThread(t *testing.T) {
	viper.Set("data_dir", "/data")
	defer viper.Reset()

	dir := telegram.ChatDir(config.DataDir(), 12345, 0)
	assert.Equal(t, "/data/12345", dir)
}

func TestChatDir_WithThread(t *testing.T) {
	viper.Set("data_dir", "/data")
	defer viper.Reset()

	dir := telegram.ChatDir(config.DataDir(), 12345, 99)
	assert.Equal(t, "/data/12345/99", dir)
}

// Forwarded message tests: verify files and text are correctly extracted from forwarded messages.

func TestMessageContent_ForwardedDocumentWithCaption(t *testing.T) {
	msg := &models.Message{
		Caption:  "forwarded file caption",
		Document: &models.Document{FileID: "fwd1", FileUniqueID: "u1", FileName: "report.pdf"},
		ForwardOrigin: &models.MessageOrigin{Type: "user"},
	}
	text, att := messageContent(msg)
	assert.Equal(t, "forwarded file caption", text)
	assert.NotNil(t, att)
	assert.Equal(t, "fwd1", att.fileID)
	assert.Equal(t, "report.pdf", att.filename)
}

func TestMessageContent_ForwardedPhotoNoCaption(t *testing.T) {
	msg := &models.Message{
		Photo: []models.PhotoSize{
			{FileID: "small", FileUniqueID: "us", Width: 100, Height: 100},
			{FileID: "large", FileUniqueID: "ul", Width: 800, Height: 800},
		},
		ForwardOrigin: &models.MessageOrigin{Type: "channel"},
	}
	text, att := messageContent(msg)
	assert.Empty(t, text)
	assert.NotNil(t, att)
	assert.Equal(t, "large", att.fileID)
	assert.Equal(t, "photo.jpg", att.filename)
}

func TestMessageContent_ForwardedVoice(t *testing.T) {
	msg := &models.Message{
		Voice: &models.Voice{FileID: "voice1", FileUniqueID: "vu1", FileSize: 12345},
		ForwardOrigin: &models.MessageOrigin{Type: "user"},
	}
	text, att := messageContent(msg)
	assert.Empty(t, text)
	assert.NotNil(t, att)
	assert.Equal(t, "voice1", att.fileID)
	assert.Equal(t, "voice.ogg", att.filename)
}

func TestMessageContent_ForwardedTextOnly(t *testing.T) {
	msg := &models.Message{
		Text: "forwarded text message",
		ForwardOrigin: &models.MessageOrigin{Type: "user"},
	}
	text, att := messageContent(msg)
	assert.Equal(t, "forwarded text message", text)
	assert.Nil(t, att)
}

func TestMessageContent_ForwardedVideoWithCaption(t *testing.T) {
	msg := &models.Message{
		Caption: "video description",
		Video:   &models.Video{FileID: "vid1", FileUniqueID: "vu1", FileName: "clip.mp4"},
		ForwardOrigin: &models.MessageOrigin{Type: "channel"},
	}
	text, att := messageContent(msg)
	assert.Equal(t, "video description", text)
	assert.NotNil(t, att)
	assert.Equal(t, "vid1", att.fileID)
	assert.Equal(t, "clip.mp4", att.filename)
}

func TestMessageContent_ForwardedAudio(t *testing.T) {
	msg := &models.Message{
		Audio: &models.Audio{FileID: "aud1", FileUniqueID: "au1", FileName: "song.mp3"},
		ForwardOrigin: &models.MessageOrigin{Type: "user"},
	}
	text, att := messageContent(msg)
	assert.Empty(t, text)
	assert.NotNil(t, att)
	assert.Equal(t, "aud1", att.fileID)
	assert.Equal(t, "song.mp3", att.filename)
}

func TestMessageContent_ForwardedSticker(t *testing.T) {
	msg := &models.Message{
		Sticker: &models.Sticker{FileID: "st1", FileUniqueID: "su1"},
		ForwardOrigin: &models.MessageOrigin{Type: "user"},
	}
	text, att := messageContent(msg)
	assert.Empty(t, text)
	assert.NotNil(t, att)
	assert.Equal(t, "st1", att.fileID)
	assert.Equal(t, "sticker.webp", att.filename)
}

func TestResolveContent_ForwardedDocumentWithCaption(t *testing.T) {
	msg := &models.Message{
		Caption:  "see this file",
		Document: &models.Document{FileID: "fwd1", FileUniqueID: "u1", FileName: "data.csv"},
		ForwardOrigin: &models.MessageOrigin{Type: "user"},
	}
	text, att := resolveContent(msg)
	assert.Equal(t, "see this file", text)
	assert.NotNil(t, att)
	assert.Equal(t, "data.csv", att.filename)
}

func TestResolveContent_ForwardedPhotoReplyFallback(t *testing.T) {
	// User sends text as reply to a forwarded photo — attachment extracted from reply.
	msg := &models.Message{
		Text: "what is this?",
		ReplyToMessage: &models.Message{
			Photo: []models.PhotoSize{
				{FileID: "p1", FileUniqueID: "pu1", Width: 800, Height: 600},
			},
			ForwardOrigin: &models.MessageOrigin{Type: "channel"},
		},
	}
	text, att := resolveContent(msg)
	assert.Equal(t, "what is this?", text)
	assert.NotNil(t, att)
	assert.Equal(t, "p1", att.fileID)
}

func TestMessageContent_CaptionWithoutAttachment(t *testing.T) {
	// Edge case: Caption field set but no media (shouldn't happen in Telegram, but test robustness).
	msg := &models.Message{
		Caption: "orphan caption",
	}
	text, att := messageContent(msg)
	assert.Equal(t, "orphan caption", text)
	assert.Nil(t, att)
}

func TestBuildPrompt_NoAttachment(t *testing.T) {
	result := buildPrompt(context.TODO(), nil, "just text", nil, "/tmp")
	assert.Equal(t, "just text", result)
}
