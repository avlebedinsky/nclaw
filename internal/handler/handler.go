package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/config"
	"github.com/nickalie/nclaw/internal/invoker"
	"github.com/nickalie/nclaw/internal/pipeline"
)

// Handler processes incoming Telegram messages.
type Handler struct {
	Invoker  *invoker.Invoker
	Pipeline *pipeline.Pipeline
}

// Default handles incoming messages by forwarding them to Claude Code.
func (h *Handler) Default(parentCtx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	msg := update.Message

	if !isChatAllowed(msg.Chat.ID) {
		log.Printf("handler: ignoring message from non-whitelisted chat=%d", msg.Chat.ID)
		return
	}

	text, att := resolveContent(msg)
	if text == "" && att == nil {
		log.Printf("handler: skipping update (no text or attachment)")
		return
	}

	go h.processMessage(parentCtx, b, msg, text, att)
}

func (h *Handler) processMessage(ctx context.Context, b *bot.Bot, msg *models.Message, text string, att *attachment) {
	text = withReplyContext(msg, text)

	chatID := msg.Chat.ID
	threadID := msg.MessageThreadID
	dir := h.Invoker.ChatDir(chatID, threadID)
	ensureDir(dir)

	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()

	go sendTyping(typingCtx, b, chatID, threadID)

	prompt := buildPrompt(ctx, b, text, att, dir)
	log.Printf("handler: received message from chat=%d thread=%d text_len=%d hasFile=%v", chatID, threadID, len(text), att != nil)

	var stream *pipeline.StreamState
	out := h.Invoker.Run(ctx, invoker.Request{
		ChatID:   chatID,
		ThreadID: threadID,
		Prompt:   prompt,
		Configure: func(c cli.Client) {
			stream = h.Pipeline.AttachStream(ctx, c, chatID, threadID)
		},
	})
	stopTyping()

	h.Pipeline.Process(ctx, withErrorText(out.Result, out.Err), out.Err, chatID, threadID, out.Dir, stream.Streamed())
}

func withErrorText(result *cli.Result, err error) *cli.Result {
	var timeout *invoker.TimeoutError
	switch {
	case err == nil:
		return result
	case errors.As(err, &timeout):
		notice := fmt.Sprintf("⏱ Stopped: the run took longer than %s.", timeout.After)
		text := strings.TrimSpace(result.Text + "\n\n" + notice)
		return &cli.Result{Text: text, FullText: result.FullText}
	case result.Text != "":
		return result
	default:
		text := "error: " + err.Error()
		return &cli.Result{Text: text, FullText: text}
	}
}

// resolveContent extracts text and attachment from a message, falling back to reply attachment.
func resolveContent(msg *models.Message) (string, *attachment) {
	text, att := messageContent(msg)
	if att == nil && msg.ReplyToMessage != nil {
		att = extractAttachment(msg.ReplyToMessage)
	}
	return text, att
}

// withReplyContext prepends the original message text when the user replies to a message.
func withReplyContext(msg *models.Message, text string) string {
	if msg.ReplyToMessage == nil {
		return text
	}

	original := msg.ReplyToMessage.Text
	if original == "" {
		original = msg.ReplyToMessage.Caption
	}

	if original == "" {
		return text
	}

	return fmt.Sprintf("[Replying to message: %s]\n\n%s", original, text)
}

// messageContent extracts the user text and optional attachment from a message.
// For regular text messages, Text is populated. For media messages (including
// forwarded ones), Caption holds the text while Text is empty.
func messageContent(msg *models.Message) (string, *attachment) {
	att := extractAttachment(msg)

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	return text, att
}

// buildPrompt constructs the prompt for Claude, downloading any attachment first.
func buildPrompt(ctx context.Context, b *bot.Bot, text string, att *attachment, dir string) string {
	if att == nil {
		return text
	}

	localPath, err := downloadAttachment(ctx, b, att, dir)
	if err != nil {
		log.Printf("handler: download error: %v", err)
		return text + "\n\n(file attachment failed to download: " + err.Error() + ")"
	}

	prompt := fmt.Sprintf("I'm sending you a file: %s (saved at %s). Please read it.\n\n", att.filename, localPath)
	if text != "" {
		prompt += text
	}

	return prompt
}

func sendTyping(ctx context.Context, b *bot.Bot, chatID int64, threadID int) {
	params := &bot.SendChatActionParams{
		ChatID:          chatID,
		MessageThreadID: threadID,
		Action:          models.ChatActionTyping,
	}

	for {
		b.SendChatAction(ctx, params) //nolint:errcheck // typing indicator is best-effort

		select {
		case <-ctx.Done():
			return
		case <-time.After(4 * time.Second):
		}
	}
}

func isChatAllowed(chatID int64) bool {
	ids := config.WhitelistChatIDs()
	return len(ids) == 0 || slices.Contains(ids, chatID)
}

func ensureDir(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("failed to create dir %s: %v", dir, err)
	}
}
