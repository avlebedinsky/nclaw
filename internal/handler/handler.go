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

	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/config"
	"github.com/nickalie/nclaw/internal/draft"
	"github.com/nickalie/nclaw/internal/invoker"
	"github.com/nickalie/nclaw/internal/pipeline"
	"github.com/nickalie/nclaw/internal/progress"
)

const maxSpeechSeconds = 600

// Handler processes incoming Telegram messages.
type Handler struct {
	Invoker     *invoker.Invoker
	Pipeline    *pipeline.Pipeline
	Queue       *chatqueue.Queue[Inbound]
	Bot         *bot.Bot
	BotUsername string
	Send        pipeline.SendFunc
	Progress    progress.MessageAPI
	Transcriber Transcriber
	React       MessageReactor
	Drafts      draft.API
	AuthStatus  func() string
	Logins      *Logins
	Buttons     ButtonAPI
	Tasks       TaskManager
}

// MessageReactor sets the bot's reaction on a message; an empty emoji clears it.
type MessageReactor func(ctx context.Context, chatID int64, msgID int, emoji string) error

const (
	reactionQueued  = "👀"
	reactionWorking = "✍"
	reactionDone    = "👌"
	reactionFailed  = "💔"
)

func (h *Handler) react(key chatqueue.Key, batch []Inbound, emoji string) {
	if h.React == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, in := range batch {
		if in.msgID == 0 {
			continue
		}
		if err := h.React(ctx, key.ChatID, in.msgID, emoji); err != nil {
			log.Printf("handler: react chat=%d msg=%d: %v", key.ChatID, in.msgID, err)
		}
	}
}

func outcomeReaction(err error) string {
	if err != nil {
		return reactionFailed
	}
	return reactionDone
}

// Transcriber turns a downloaded voice message or video note into text.
type Transcriber interface {
	Transcribe(ctx context.Context, path string) (string, error)
}

// AllowChat is bot middleware that drops updates nclaw does not handle and updates
// from chats outside the whitelist.
func AllowChat(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		chatID, ok := updateChatID(update)
		if !ok {
			return
		}
		if !isChatAllowed(chatID) {
			log.Printf("handler: ignoring update from non-whitelisted chat=%d", chatID)
			return
		}
		next(ctx, b, update)
	}
}

func updateChatID(update *models.Update) (int64, bool) {
	switch {
	case update.Message != nil:
		return update.Message.Chat.ID, true
	case update.StoppedMessageGeneration != nil:
		return update.StoppedMessageGeneration.Chat.ID, true
	case update.CallbackQuery != nil:
		return callbackChatID(&update.CallbackQuery.Message)
	default:
		return 0, false
	}
}

func callbackChatID(m *models.MaybeInaccessibleMessage) (int64, bool) {
	switch {
	case m.Message != nil:
		return m.Message.Chat.ID, true
	case m.InaccessibleMessage != nil:
		return m.InaccessibleMessage.Chat.ID, true
	default:
		return 0, false
	}
}

// Default queues an incoming message for its chat; it never blocks on the CLI.
func (h *Handler) Default(_ context.Context, _ *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil {
		return
	}
	h.rememberChat(msg)
	if _, forMe, isCmd := parseCommand(msg.Text, h.BotUsername); isCmd && !forMe {
		return
	}
	if h.takeLoginCode(msg) {
		return
	}

	in, ok := newInbound(msg)
	if !ok {
		log.Printf("handler: skipping update (no text or attachment)")
		return
	}

	key := chatqueue.Key{ChatID: msg.Chat.ID, ThreadID: msg.MessageThreadID}
	if err := h.Queue.Enqueue(key, in, settleFor(msg)); err != nil {
		log.Printf("handler: chat=%d thread=%d message not queued: %v", key.ChatID, key.ThreadID, err)
		return
	}
	go h.react(key, []Inbound{in}, reactionQueued)
}

// RunBatch answers a batch of queued messages from one chat with a single CLI run.
func (h *Handler) RunBatch(ctx context.Context, key chatqueue.Key, batch []Inbound) {
	dir := h.Invoker.ChatDir(key.ChatID, key.ThreadID)
	ensureDir(dir)
	h.react(key, batch, reactionWorking)

	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go sendTyping(typingCtx, h.Bot, key.ChatID, key.ThreadID)

	prompt := h.composePrompt(ctx, dir, batch)
	log.Printf("handler: running %d message(s) for chat=%d thread=%d prompt_len=%d", len(batch), key.ChatID, key.ThreadID, len(prompt))

	dest := pipeline.Dest{ChatID: key.ChatID, ThreadID: key.ThreadID, ReplyTo: batch[len(batch)-1].msgID}
	out, streamed := h.run(ctx, key, dest, prompt)
	stopTyping()

	if errors.Is(out.Err, chatqueue.ErrStopped) || errors.Is(out.Err, chatqueue.ErrShuttingDown) {
		log.Printf("handler: run for chat=%d thread=%d interrupted: %v", key.ChatID, key.ThreadID, out.Err)
		h.react(key, batch, "")
		return
	}

	deliverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	h.Pipeline.Process(deliverCtx, withErrorText(out.Result, out.Err), out.Err, dest, out.Dir, streamed)
	h.react(key, batch, outcomeReaction(out.Err))
}

func (h *Handler) run(ctx context.Context, key chatqueue.Key, dest pipeline.Dest, prompt string) (invoker.Outcome, bool) {
	var stream *pipeline.StreamState
	reporter := h.newReporter(key)
	drafter := h.newDrafter(key)
	out := h.Invoker.Run(ctx, invoker.Request{
		ChatID:   key.ChatID,
		ThreadID: key.ThreadID,
		Prompt:   prompt,
		Configure: func(c cli.Client) {
			stream = h.Pipeline.AttachStream(ctx, c, dest)
			attachLiveViews(c, reporter, drafter)
		},
	})
	drafter.Finish()
	reporter.Finish(context.WithoutCancel(ctx))
	return out, stream.Streamed()
}

func attachLiveViews(c cli.Client, reporter *progress.Reporter, drafter *draft.Drafter) {
	if pc, ok := c.(cli.ProgressClient); ok && reporter != nil {
		pc.OnToolUse(reporter.OnTool)
	}
	if pc, ok := c.(cli.PartialClient); ok && drafter != nil {
		pc.OnPartialText(drafter.Update)
	}
}

func (h *Handler) newDrafter(key chatqueue.Key) *draft.Drafter {
	if h.Drafts == nil || key.ChatID <= 0 {
		return nil
	}
	return draft.New(h.Drafts, key.ChatID, key.ThreadID, draft.Options{})
}

func (h *Handler) newReporter(key chatqueue.Key) *progress.Reporter {
	if h.Progress == nil {
		return nil
	}
	return progress.New(h.Progress, key.ChatID, key.ThreadID, progress.Options{})
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

func (h *Handler) buildPrompt(ctx context.Context, text string, att *attachment, dir string) string {
	if att == nil {
		return text
	}

	localPath, err := downloadAttachment(ctx, h.Bot, att, dir)
	if err != nil {
		log.Printf("handler: download error: %v", err)
		return text + "\n\n(file attachment failed to download: " + err.Error() + ")"
	}

	if transcript := h.transcribe(ctx, att, localPath); transcript != "" {
		return joinNonEmpty("[Voice message, transcribed]: "+transcript,
			"(Transcribed locally from "+localPath+". Treat the text above as my message; the audio does not need to be transcribed again.)",
			text)
	}

	prompt := fmt.Sprintf("I'm sending you a file: %s (saved at %s). Please read it.\n\n", att.filename, localPath)
	if text != "" {
		prompt += text
	}

	return prompt
}

func (h *Handler) transcribe(ctx context.Context, att *attachment, path string) string {
	if h.Transcriber == nil || !att.speech || att.duration > maxSpeechSeconds {
		return ""
	}

	start := time.Now()
	text, err := h.Transcriber.Transcribe(ctx, path)
	if err != nil {
		log.Printf("handler: transcribe %s: %v", path, err)
		return ""
	}
	log.Printf("handler: transcribed %ds of audio in %s (%d chars)", att.duration, time.Since(start).Round(time.Millisecond), len(text))
	return text
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
