package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/config"
	"github.com/nickalie/nclaw/internal/invoker"
	"github.com/nickalie/nclaw/internal/pipeline"
	"github.com/nickalie/nclaw/internal/sendfile"
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
		Caption:       "forwarded file caption",
		Document:      &models.Document{FileID: "fwd1", FileUniqueID: "u1", FileName: "report.pdf"},
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
		Voice:         &models.Voice{FileID: "voice1", FileUniqueID: "vu1", FileSize: 12345},
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
		Text:          "forwarded text message",
		ForwardOrigin: &models.MessageOrigin{Type: "user"},
	}
	text, att := messageContent(msg)
	assert.Equal(t, "forwarded text message", text)
	assert.Nil(t, att)
}

func TestMessageContent_ForwardedVideoWithCaption(t *testing.T) {
	msg := &models.Message{
		Caption:       "video description",
		Video:         &models.Video{FileID: "vid1", FileUniqueID: "vu1", FileName: "clip.mp4"},
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
		Audio:         &models.Audio{FileID: "aud1", FileUniqueID: "au1", FileName: "song.mp3"},
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
		Sticker:       &models.Sticker{FileID: "st1", FileUniqueID: "su1"},
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
		Caption:       "see this file",
		Document:      &models.Document{FileID: "fwd1", FileUniqueID: "u1", FileName: "data.csv"},
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

// --- isCached tests ---

func TestIsCached_FileNotFound(t *testing.T) {
	att := &attachment{fileUniqueID: "uid1", fileSize: 100}
	assert.False(t, isCached(filepath.Join(t.TempDir(), "missing.txt"), att))
}

func TestIsCached_MatchingSizeAndUID(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "file.txt")
	content := []byte("hello")
	require.NoError(t, os.WriteFile(localPath, content, 0o644))
	require.NoError(t, os.WriteFile(localPath+".uid", []byte("uid1"), 0o644))

	att := &attachment{fileUniqueID: "uid1", fileSize: int64(len(content))}
	assert.True(t, isCached(localPath, att))
}

func TestIsCached_MismatchingSize(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(localPath, []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(localPath+".uid", []byte("uid1"), 0o644))

	att := &attachment{fileUniqueID: "uid1", fileSize: 999}
	assert.False(t, isCached(localPath, att))
}

func TestIsCached_MismatchingUID(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "file.txt")
	content := []byte("hello")
	require.NoError(t, os.WriteFile(localPath, content, 0o644))
	require.NoError(t, os.WriteFile(localPath+".uid", []byte("uid1"), 0o644))

	att := &attachment{fileUniqueID: "different-uid", fileSize: int64(len(content))}
	assert.False(t, isCached(localPath, att))
}

func TestIsCached_ZeroSizeAttachment(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(localPath, []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(localPath+".uid", []byte("uid1"), 0o644))

	// fileSize == 0 means size check is skipped, only UID matters.
	att := &attachment{fileUniqueID: "uid1", fileSize: 0}
	assert.True(t, isCached(localPath, att))
}

// --- writeUID tests ---

func TestWriteUID_WritesFile(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(localPath, []byte("data"), 0o644))

	writeUID(localPath, "my-unique-id")

	stored, err := os.ReadFile(localPath + ".uid")
	require.NoError(t, err)
	assert.Equal(t, "my-unique-id", string(stored))
}

func TestWriteUID_EmptyUIDDoesNothing(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "file.txt")

	writeUID(localPath, "")

	_, err := os.Stat(localPath + ".uid")
	assert.True(t, os.IsNotExist(err))
}

// --- fetchToFile tests ---

func TestFetchToFile_Success(t *testing.T) {
	expected := "file content from server"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(expected))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "downloaded.txt")

	err := fetchToFile(context.Background(), srv.URL, dst)
	require.NoError(t, err)

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, expected, string(got))
}

func TestFetchToFile_Non200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "downloaded.txt")

	err := fetchToFile(context.Background(), srv.URL, dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 404")
}

// --- ensureDir tests ---

func TestEnsureDir_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	_, err := os.Stat(dir)
	require.True(t, os.IsNotExist(err))

	ensureDir(dir)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureDir_AlreadyExists(t *testing.T) {
	dir := t.TempDir()

	// Should not panic or error when dir already exists.
	ensureDir(dir)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestBuildPrompt_WithAttachment(t *testing.T) {
	// When attachment is nil, buildPrompt should return plain text unchanged.
	result := buildPrompt(context.TODO(), nil, "some text", nil, t.TempDir())
	assert.Equal(t, "some text", result)
}

func TestResolveContent_TextWithReplyAttachment(t *testing.T) {
	// Message has text, reply has a document — attachment should come from reply.
	msg := &models.Message{
		Text: "please check this",
		ReplyToMessage: &models.Message{
			Document: &models.Document{FileID: "rdoc1", FileUniqueID: "ru1", FileName: "reply.pdf"},
		},
	}
	text, att := resolveContent(msg)
	assert.Equal(t, "please check this", text)
	assert.NotNil(t, att)
	assert.Equal(t, "rdoc1", att.fileID)
	assert.Equal(t, "reply.pdf", att.filename)
}

func TestResolveContent_NoTextNoAttachment(t *testing.T) {
	msg := &models.Message{}
	text, att := resolveContent(msg)
	assert.Empty(t, text)
	assert.Nil(t, att)
}

func TestResolveContent_TextOnly(t *testing.T) {
	msg := &models.Message{Text: "just some text"}
	text, att := resolveContent(msg)
	assert.Equal(t, "just some text", text)
	assert.Nil(t, att)
}

func TestFetchToFile_InvalidURL(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.txt")

	err := fetchToFile(context.Background(), "http://invalid.localhost:1/nope", dst)
	assert.Error(t, err)
}

// --- mock CLI types ---

type mockClient struct {
	dir          string
	skipPerms    bool
	systemPrompt string
	askResult    *cli.Result
	askErr       error
	contResult   *cli.Result
	contErr      error
	lastQuery    string
}

func (m *mockClient) Dir(dir string) cli.Client              { m.dir = dir; return m }
func (m *mockClient) Context(context.Context) cli.Client     { return m }
func (m *mockClient) SkipPermissions() cli.Client            { m.skipPerms = true; return m }
func (m *mockClient) AppendSystemPrompt(p string) cli.Client { m.systemPrompt = p; return m }
func (m *mockClient) Ask(query string) (*cli.Result, error) {
	m.lastQuery = query
	return m.askResult, m.askErr
}
func (m *mockClient) Continue(query string) (*cli.Result, error) {
	m.lastQuery = query
	return m.contResult, m.contErr
}

type mockProvider struct {
	client       *mockClient
	preInvokeErr error
	name         string
}

func (m *mockProvider) NewClient() cli.Client    { return m.client }
func (m *mockProvider) PreInvoke() error         { return m.preInvokeErr }
func (m *mockProvider) Version() (string, error) { return "mock-1.0.0", nil }
func (m *mockProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

// newTestBot creates a bot.Bot backed by a fake HTTP server.
func newTestBot(t *testing.T) *bot.Bot {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getMe") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	t.Cleanup(srv.Close)

	b, err := bot.New("test-token", bot.WithServerURL(srv.URL))
	require.NoError(t, err)
	return b
}

func newTestHandler(t *testing.T, provider cli.Provider, send pipeline.SendFunc) *Handler {
	t.Helper()
	inv := invoker.New(provider, invoker.Options{DataDir: t.TempDir()})
	h := &Handler{Invoker: inv, Pipeline: pipeline.New(send, sendfile.Senders{}, false), Bot: newTestBot(t)}
	h.Queue = chatqueue.New(h.RunBatch, chatqueue.Options{})
	return h
}

func waitQueue(t *testing.T, h *Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, h.Queue.Wait(ctx))
}

func recordSend(sent *[]string) pipeline.SendFunc {
	return func(_ context.Context, _ int64, _ int, text, _ string) error {
		*sent = append(*sent, text)
		return nil
	}
}

// --- Default tests ---

func TestDefault_NilMessage(t *testing.T) {
	h := &Handler{}
	update := &models.Update{Message: nil}
	// Should return immediately without panic.
	h.Default(context.Background(), newTestBot(t), update)
}

func TestAllowChat(t *testing.T) {
	viper.Set("telegram.whitelist_chat_ids", "111,222")
	defer viper.Reset()

	var passed []int64
	next := AllowChat(func(_ context.Context, _ *bot.Bot, u *models.Update) { passed = append(passed, u.Message.Chat.ID) })
	for _, u := range []*models.Update{
		{Message: &models.Message{Text: "hi", Chat: models.Chat{ID: 999}}},
		{Message: &models.Message{Text: "hi", Chat: models.Chat{ID: 111}}},
		{},
	} {
		next(context.Background(), nil, u)
	}

	assert.Equal(t, []int64{111}, passed)
}

func TestDefault_BurstIsAnsweredOnce(t *testing.T) {
	client := &mockClient{contResult: &cli.Result{Text: "both done", FullText: "both done"}}
	var sent []string
	h := newTestHandler(t, &mockProvider{client: client}, recordSend(&sent))

	for _, text := range []string{"first", "second"} {
		h.Default(context.Background(), nil, &models.Update{Message: &models.Message{Text: text, Chat: models.Chat{ID: 100}}})
	}
	waitQueue(t, h)

	assert.Equal(t, []string{"both done"}, sent)
	assert.Contains(t, client.lastQuery, "--- Message 1 ---\nfirst")
	assert.Contains(t, client.lastQuery, "--- Message 2 ---\nsecond")
}

func TestDefault_EmptyMessage(t *testing.T) {
	viper.Reset()

	h := &Handler{}
	update := &models.Update{
		Message: &models.Message{
			Chat: models.Chat{ID: 100},
		},
	}
	// Empty message (no text, no attachment) should return early.
	h.Default(context.Background(), newTestBot(t), update)
}

// --- buildPrompt tests ---

func TestBuildPrompt_WithDownloadError(t *testing.T) {
	// Create a bot that returns an error for GetFile.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getMe") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":false,"description":"file not found"}`))
	}))
	defer srv.Close()

	b, err := bot.New("test-token", bot.WithServerURL(srv.URL))
	require.NoError(t, err)

	att := &attachment{fileID: "f1", filename: "test.pdf"}
	result := buildPrompt(context.Background(), b, "analyze this", att, t.TempDir())
	// Should contain original text plus error message.
	assert.Contains(t, result, "analyze this")
	assert.Contains(t, result, "file attachment failed to download")
}

func TestBuildPrompt_WithSuccessfulDownload(t *testing.T) {
	fileContent := "file data here"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getFile") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"f1","file_path":"docs/test.pdf"}}`))
			return
		}
		if strings.Contains(r.URL.Path, "file/") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(fileContent))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	b, err := bot.New("test-token", bot.WithServerURL(srv.URL))
	require.NoError(t, err)

	dir := t.TempDir()
	att := &attachment{fileID: "f1", filename: "test.pdf"}
	result := buildPrompt(context.Background(), b, "analyze this", att, dir)
	assert.Contains(t, result, "I'm sending you a file: test.pdf")
	assert.Contains(t, result, "analyze this")

	// Verify the file was downloaded.
	downloaded, err := os.ReadFile(filepath.Join(dir, "test.pdf"))
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(downloaded))
}

func TestBuildPrompt_WithAttachmentNoText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getFile") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"f1","file_path":"docs/photo.jpg"}}`))
			return
		}
		if strings.Contains(r.URL.Path, "file/") {
			_, _ = w.Write([]byte("image data"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	b, err := bot.New("test-token", bot.WithServerURL(srv.URL))
	require.NoError(t, err)

	att := &attachment{fileID: "f1", filename: "photo.jpg"}
	result := buildPrompt(context.Background(), b, "", att, t.TempDir())
	assert.Contains(t, result, "I'm sending you a file: photo.jpg")
	// No trailing user text appended.
	assert.NotContains(t, result, "\n\n\n")
}

// --- RunBatch tests ---

var testKey = chatqueue.Key{ChatID: 100}

func TestRunBatch_Success(t *testing.T) {
	client := &mockClient{contResult: &cli.Result{Text: "cli response", FullText: "cli response"}}
	var sent []string
	h := newTestHandler(t, &mockProvider{client: client}, recordSend(&sent))

	h.RunBatch(context.Background(), testKey, []Inbound{{text: "hello"}})

	assert.Equal(t, []string{"cli response"}, sent)
	assert.True(t, strings.HasSuffix(client.lastQuery, "\n\nhello"))
	assert.Equal(t, h.Invoker.ChatDir(100, 0), client.dir)
	assert.Contains(t, client.systemPrompt, telegram.Prompt)
}

func TestRunBatch_CLIError(t *testing.T) {
	client := &mockClient{contErr: fmt.Errorf("boom")}
	var sent []string
	h := newTestHandler(t, &mockProvider{client: client}, recordSend(&sent))

	h.RunBatch(context.Background(), testKey, []Inbound{{text: "hello"}})

	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "error: boom")
}

func TestRunBatch_CLIErrorKeepsPartialOutput(t *testing.T) {
	client := &mockClient{contResult: &cli.Result{Text: "partial output", FullText: "partial output"}, contErr: fmt.Errorf("timeout")}
	var sent []string
	h := newTestHandler(t, &mockProvider{client: client}, recordSend(&sent))

	h.RunBatch(context.Background(), testKey, []Inbound{{text: "hello"}})

	assert.Equal(t, []string{"partial output"}, sent)
}

func TestRunBatch_StoppedRunSendsNothing(t *testing.T) {
	for _, cause := range []error{chatqueue.ErrStopped, chatqueue.ErrShuttingDown} {
		client := &mockClient{contResult: &cli.Result{Text: "half"}, contErr: fmt.Errorf("claude: %w", cause)}
		var sent []string
		h := newTestHandler(t, &mockProvider{client: client}, recordSend(&sent))

		h.RunBatch(context.Background(), testKey, []Inbound{{text: "hello"}})

		assert.Empty(t, sent, cause.Error())
	}
}

func TestNewInbound_IncludesReplyContext(t *testing.T) {
	msg := &models.Message{Text: "and this?", Chat: models.Chat{ID: 100}, ReplyToMessage: &models.Message{Text: "earlier"}, MediaGroupID: "g1"}

	in, ok := newInbound(msg)

	require.True(t, ok)
	assert.Equal(t, "[Replying to message: earlier]\n\nand this?", in.text)
	assert.Equal(t, "g1", in.mediaGroup)
	_, ok = newInbound(&models.Message{Chat: models.Chat{ID: 1}})
	assert.False(t, ok)
}

func TestSettleFor(t *testing.T) {
	assert.Equal(t, messageSettle, settleFor(&models.Message{}))
	assert.Equal(t, albumSettle, settleFor(&models.Message{MediaGroupID: "g"}))
}

func TestComposePrompt_SingleMessageUnchanged(t *testing.T) {
	assert.Equal(t, "hello", composePrompt(context.Background(), nil, t.TempDir(), []Inbound{{text: "hello"}}))
}

func TestComposePrompt_AlbumWithCaptionAndFollowUp(t *testing.T) {
	srv := newFileServer(t, map[string]string{"f1": "one", "f2": "two"})
	b, err := bot.New("123:SECRET", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	require.NoError(t, err)
	dir := t.TempDir()

	prompt := composePrompt(context.Background(), b, dir, []Inbound{
		{text: "what is on these?", att: &attachment{fileID: "f1", fileUniqueID: "u1", filename: "photo.jpg"}, mediaGroup: "g1"},
		{att: &attachment{fileID: "f2", fileUniqueID: "u2", filename: "photo.jpg"}, mediaGroup: "g1"},
		{text: "and compare them"},
	})

	assert.Contains(t, prompt, "The user sent 2 messages in a row")
	assert.Contains(t, prompt, "--- Message 1 ---\nI'm sending you 2 files: photo.jpg (saved at "+filepath.Join(dir, "photo_u1.jpg")+
		"), photo.jpg (saved at "+filepath.Join(dir, "photo_u2.jpg")+"). Please read them.\n\nwhat is on these?")
	assert.Contains(t, prompt, "--- Message 2 ---\nand compare them")
}

func TestComposePrompt_AlbumDownloadFailure(t *testing.T) {
	srv := newFileServer(t, map[string]string{"f1": "one"})
	b, err := bot.New("123:SECRET", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	require.NoError(t, err)

	prompt := composePrompt(context.Background(), b, t.TempDir(), []Inbound{
		{att: &attachment{fileID: "f1", fileUniqueID: "u1", filename: "a.jpg"}, mediaGroup: "g"},
		{att: &attachment{fileID: "missing", fileUniqueID: "u2", filename: "b.jpg"}, mediaGroup: "g"},
	})

	assert.Contains(t, prompt, "I'm sending you 1 files: a.jpg")
	assert.Contains(t, prompt, "(these attachments failed to download: b.jpg")
	assert.NotContains(t, prompt, "SECRET")
}

// --- sendTyping tests ---

func TestSendTyping_StopsOnCancel(t *testing.T) {
	var called int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getMe") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()

	b, err := bot.New("test-token", bot.WithServerURL(srv.URL))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sendTyping(ctx, b, 100, 0)
		close(done)
	}()

	// Let it send at least one typing action, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// sendTyping returned after cancel.
	case <-time.After(2 * time.Second):
		t.Fatal("sendTyping did not stop after context cancel")
	}
	assert.GreaterOrEqual(t, called, 1, "should have called sendChatAction at least once")
}

// --- attachment naming and token redaction tests ---

func TestLocalName(t *testing.T) {
	cases := []struct {
		name, filename, uid, want string
	}{
		{"photo with uid", "photo.jpg", "AgADx1", "photo_AgADx1.jpg"},
		{"no uid keeps name", "report.pdf", "", "report.pdf"},
		{"strips directories", "../../etc/passwd", "u1", "passwd_u1"},
		{"empty name", "", "u1", "file_u1"},
		{"dot dot", "..", "u1", "file_u1"},
		{"multiple dots", "archive.tar.gz", "u1", "archive.tar_u1.gz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, localName(&attachment{filename: tc.filename, fileUniqueID: tc.uid}))
		})
	}
}

func newFileServer(t *testing.T, contents map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "getFile") {
			require.NoError(t, r.ParseMultipartForm(1<<20))
			id := r.FormValue("file_id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"file_id":%q,"file_path":"photos/%s.jpg"}}`, id, id)
			return
		}
		for id, body := range contents {
			if strings.HasSuffix(r.URL.Path, "/photos/"+id+".jpg") {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDownloadAttachment_AlbumPhotosDoNotOverwrite(t *testing.T) {
	srv := newFileServer(t, map[string]string{"f1": "first", "f2": "second"})
	b, err := bot.New("123:SECRET", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	require.NoError(t, err)
	dir := t.TempDir()

	atts := []*attachment{
		{fileID: "f1", fileUniqueID: "u1", filename: "photo.jpg"},
		{fileID: "f2", fileUniqueID: "u2", filename: "photo.jpg"},
	}
	paths := make([]string, len(atts))
	var wg sync.WaitGroup
	for i, att := range atts {
		wg.Go(func() {
			p, err := downloadAttachment(context.Background(), b, att, dir)
			assert.NoError(t, err)
			paths[i] = p
		})
	}
	wg.Wait()

	require.NotEqual(t, paths[0], paths[1])
	for i, want := range []string{"first", "second"} {
		got, err := os.ReadFile(paths[i])
		require.NoError(t, err)
		assert.Equal(t, want, string(got))
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "*.part"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestDownloadAttachment_DoesNotLogBotToken(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	srv := newFileServer(t, map[string]string{"f1": "data"})
	b, err := bot.New("123:SECRET", bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	require.NoError(t, err)

	_, err = downloadAttachment(context.Background(), b, &attachment{fileID: "f1", fileUniqueID: "u1", filename: "photo.jpg"}, t.TempDir())
	require.NoError(t, err)

	assert.NotContains(t, logs.String(), "SECRET")
}

func TestFetchToFile_ErrorOmitsURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	link := srv.URL + "/file/bot123:SECRET/photos/f1.jpg"
	srv.Close()

	err := fetchToFile(context.Background(), link, filepath.Join(t.TempDir(), "out.jpg"))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
}

func TestWithErrorText(t *testing.T) {
	timeout := fmt.Errorf("claude: %w", &invoker.TimeoutError{After: time.Hour})

	assert.Equal(t, "ok", withErrorText(&cli.Result{Text: "ok"}, nil).Text)
	assert.Equal(t, "partial", withErrorText(&cli.Result{Text: "partial"}, errors.New("x")).Text)
	assert.Equal(t, "error: boom", withErrorText(&cli.Result{}, errors.New("boom")).Text)
	assert.Equal(t, "⏱ Stopped: the run took longer than 1h0m0s.", withErrorText(&cli.Result{}, timeout).Text)
	assert.Equal(t, "half done\n\n⏱ Stopped: the run took longer than 1h0m0s.", withErrorText(&cli.Result{Text: "half done"}, timeout).Text)
}

type progressClient struct {
	mockClient
	onTool cli.ToolHandler
}

func (c *progressClient) Context(context.Context) cli.Client   { return c }
func (c *progressClient) Dir(string) cli.Client                { return c }
func (c *progressClient) SkipPermissions() cli.Client          { return c }
func (c *progressClient) AppendSystemPrompt(string) cli.Client { return c }
func (c *progressClient) OnToolUse(h cli.ToolHandler) cli.Client {
	c.onTool = h
	return c
}
func (c *progressClient) Continue(string) (*cli.Result, error) {
	c.onTool(cli.ToolEvent{Name: "Bash", Detail: "make test"})
	time.Sleep(50 * time.Millisecond)
	return &cli.Result{Text: "tests pass", FullText: "tests pass"}, nil
}

type progressProvider struct {
	mockProvider
	client *progressClient
}

func (p *progressProvider) NewClient() cli.Client { return p.client }

type progressAPI struct {
	mu  sync.Mutex
	ops []string
}

func (a *progressAPI) Send(_ context.Context, _ int64, _ int, text string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ops = append(a.ops, "send:"+text[:strings.Index(text, "\n")])
	return 9, nil
}
func (a *progressAPI) Edit(context.Context, int64, int, string) error { return nil }
func (a *progressAPI) Delete(_ context.Context, _ int64, msgID int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ops = append(a.ops, fmt.Sprintf("delete:%d", msgID))
	return nil
}

func TestRunBatch_ShowsAndRemovesProgress(t *testing.T) {
	api := &progressAPI{}
	sent := &safeSent{}
	h := newTestHandler(t, &progressProvider{client: &progressClient{}}, sent.send)
	h.Progress = api

	h.RunBatch(context.Background(), testKey, []Inbound{{text: "run tests"}})

	assert.Equal(t, []string{"send:🔧 Bash: make test", "delete:9"}, api.ops)
	assert.Equal(t, []string{"tests pass"}, sent.all())
}
