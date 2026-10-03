package pipeline

import (
	"context"
	"log"
	"strings"

	"github.com/nickalie/nclaw/internal/blocks"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/sendfile"
	"github.com/nickalie/nclaw/internal/telegram"
)

// BlockExecutor processes command blocks extracted from Claude's response.
// ExecuteBlocks scans text for command blocks, executes them, and returns
// any status/error messages to append to the display text.
type BlockExecutor interface {
	ExecuteBlocks(text string, chatID int64, threadID int) string
}

// Dest addresses a message: the chat, its forum thread and, when non-zero, the
// message being answered.
type Dest struct {
	ChatID   int64
	ThreadID int
	ReplyTo  int
}

// SendFunc sends a text message to a destination with an optional parse mode.
type SendFunc func(ctx context.Context, dest Dest, text, parseMode string) error

const (
	maxReplyChunks    = 3
	longAnswerCaption = "📄 The full answer is in the file"
)

// Pipeline orchestrates post-Claude response processing: block execution,
// stripping, status appending, file sending, and reply delivery.
type Pipeline struct {
	executors          []BlockExecutor
	senders            sendfile.Senders
	send               SendFunc
	webhooksConfigured bool
	streamMessages     bool
	dataDir            string
}

// New creates a Pipeline. Nil executors are silently filtered out.
// webhooksConfigured indicates whether a webhook executor is present, used to
// warn users when webhook blocks appear but webhooks are not enabled.
func New(
	send SendFunc, senders sendfile.Senders,
	webhooksConfigured bool, executors ...BlockExecutor,
) *Pipeline {
	var filtered []BlockExecutor
	for _, e := range executors {
		if e != nil {
			filtered = append(filtered, e)
		}
	}
	return &Pipeline{
		executors:          filtered,
		senders:            senders,
		send:               send,
		webhooksConfigured: webhooksConfigured,
	}
}

// SetStreamMessages controls whether every assistant message from the CLI's
// output is delivered as a separate reply (true) or only the final message
// (false, default).
func (p *Pipeline) SetStreamMessages(enabled bool) {
	p.streamMessages = enabled
}

// SetDataDir sets the base data directory; files sent from a run must lie under
// the chat's directory within it. Without it, the run's own directory is the limit.
func (p *Pipeline) SetDataDir(dir string) {
	p.dataDir = dir
}

func (p *Pipeline) chatRoot(chatID int64, threadID int, dir string) string {
	if p.dataDir == "" {
		return dir
	}
	return telegram.ChatDir(p.dataDir, chatID, threadID)
}

// StreamState tracks a live-streaming session created by AttachStream. It records
// how many messages were delivered so Process knows whether to skip re-sending.
type StreamState struct {
	sent int
}

// Streamed reports whether at least one message was delivered live. It is
// nil-safe: a nil *StreamState (streaming not attached) reports false.
func (s *StreamState) Streamed() bool {
	return s != nil && s.sent > 0
}

// AttachStream wires live per-message delivery to a client when stream output is
// enabled and the client supports streaming (cli.StreamingClient). Each assistant
// message is stripped of command blocks and sent as it arrives. Returns a
// StreamState (nil if streaming was not attached) to pass alongside the eventual
// Process call. The returned state must be read only after the CLI call returns.
func (p *Pipeline) AttachStream(ctx context.Context, client cli.Client, dest Dest) *StreamState {
	if p == nil || !p.streamMessages {
		return nil
	}

	sc, ok := client.(cli.StreamingClient)
	if !ok {
		return nil
	}

	st := &StreamState{}
	sc.OnMessage(func(msg string) {
		if text := blocks.StripAll(msg); text != "" {
			st.sent++
			p.sendReply(ctx, dest, text)
		}
	})
	return st
}

// Process handles the full post-Claude response workflow:
//  1. Execute command blocks on FullText (only when cliErr is nil)
//  2. Strip all command block syntax from Text
//  3. Append execution status messages
//  4. Send the reply with HTML-then-plain-text fallback
//
// When streamed is true, the assistant messages were already delivered live via
// AttachStream, so display messages are not re-sent; only block execution and
// status messages are handled.
func (p *Pipeline) Process(
	ctx context.Context, result *cli.Result, cliErr error,
	dest Dest, dir string, streamed bool,
) {
	// Phase 1: Execute command blocks (only on success).
	statusMsgs := p.executeBlocks(ctx, result, cliErr, dest.ChatID, dest.ThreadID, dir)

	// Phase 2: Strip all command block syntax from each display message.
	// When already streamed live, skip re-sending the display messages.
	var texts []string
	if !streamed {
		texts = p.displayTexts(result)
	}

	// Phase 3: Append status messages from block execution to the last message.
	texts = appendStatusToLast(texts, statusMsgs)

	// Phase 4: Send each reply.
	for _, text := range texts {
		if text != "" {
			p.sendReply(ctx, dest, text)
			dest.ReplyTo = 0
		}
	}
}

// executeBlocks runs command-block executors on FullText (only on success) and
// returns the status messages to append, including any webhook-not-configured warning.
func (p *Pipeline) executeBlocks(
	ctx context.Context, result *cli.Result, cliErr error,
	chatID int64, threadID int, dir string,
) []string {
	var statusMsgs []string

	if cliErr == nil {
		for _, exec := range p.executors {
			if msg := exec.ExecuteBlocks(result.FullText, chatID, threadID); msg != "" {
				statusMsgs = append(statusMsgs, msg)
			}
		}
		sendfile.ExecuteBlocks(ctx, p.senders, result.FullText, chatID, threadID, dir, p.chatRoot(chatID, threadID, dir))
	}

	if !p.webhooksConfigured && blocks.Webhook.MatchString(result.Text) {
		statusMsgs = append(statusMsgs, "[Webhooks are not configured on this instance]")
	}

	return statusMsgs
}

// displayTexts returns the stripped, non-empty messages to send. When stream
// messages are enabled and the result carries individual messages, each is sent
// separately; otherwise only the final Text is sent.
func (p *Pipeline) displayTexts(result *cli.Result) []string {
	if p.streamMessages && len(result.Messages) > 0 {
		var texts []string
		for _, m := range result.Messages {
			if s := blocks.StripAll(m); s != "" {
				texts = append(texts, s)
			}
		}
		if len(texts) > 0 {
			return texts
		}
	}

	if s := blocks.StripAll(result.Text); s != "" {
		return []string{s}
	}
	return nil
}

func appendStatus(text string, msgs []string) string {
	for _, msg := range msgs {
		if msg != "" {
			text = strings.TrimSpace(text) + "\n\n" + msg
		}
	}
	return strings.TrimSpace(text)
}

// appendStatusToLast appends status messages to the last display message. When
// there are no display messages, the status becomes a standalone message.
func appendStatusToLast(texts, msgs []string) []string {
	hasStatus := false
	for _, msg := range msgs {
		if msg != "" {
			hasStatus = true
			break
		}
	}
	if !hasStatus {
		return texts
	}

	if len(texts) == 0 {
		return []string{appendStatus("", msgs)}
	}

	last := len(texts) - 1
	texts[last] = appendStatus(texts[last], msgs)
	return texts
}

func (p *Pipeline) sendReply(ctx context.Context, dest Dest, text string) {
	log.Printf("pipeline: sending reply len=%d", len(text))
	chunks := telegram.SplitMessage(text, telegram.MaxMessageLen)
	if len(chunks) <= maxReplyChunks || p.senders.Doc == nil {
		p.sendChunks(ctx, dest, chunks)
		return
	}

	p.sendChunk(ctx, dest, chunks[0])
	file := []byte(telegram.Markdown(text))
	if err := p.senders.Doc(ctx, dest.ChatID, dest.ThreadID, "answer.md", file, longAnswerCaption); err != nil {
		log.Printf("pipeline: send long answer as file: %v", err)
		dest.ReplyTo = 0
		p.sendChunks(ctx, dest, chunks[1:])
	}
}

func (p *Pipeline) sendChunks(ctx context.Context, dest Dest, chunks []string) {
	for _, chunk := range chunks {
		p.sendChunk(ctx, dest, chunk)
		dest.ReplyTo = 0
	}
}

func (p *Pipeline) sendChunk(ctx context.Context, dest Dest, text string) {
	err := p.send(ctx, dest, text, "HTML")
	if err == nil {
		return
	}
	log.Printf("pipeline: send parseMode=HTML error: %v", err)

	if err := p.send(ctx, dest, telegram.PlainText(text), ""); err != nil {
		log.Printf("pipeline: failed to send message to chat=%d thread=%d as plain text: %v", dest.ChatID, dest.ThreadID, err)
	}
}
