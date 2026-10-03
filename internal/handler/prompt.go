package handler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	messageSettle = 700 * time.Millisecond
	albumSettle   = 1500 * time.Millisecond
)

// Inbound is a user message waiting in its chat's queue.
type Inbound struct {
	text       string
	att        *attachment
	mediaGroup string
}

func newInbound(msg *models.Message) (Inbound, bool) {
	text, att := resolveContent(msg)
	if text == "" && att == nil {
		return Inbound{}, false
	}
	return Inbound{text: withReplyContext(msg, text), att: att, mediaGroup: msg.MediaGroupID}, true
}

func settleFor(msg *models.Message) time.Duration {
	if msg.MediaGroupID != "" {
		return albumSettle
	}
	return messageSettle
}

func composePrompt(ctx context.Context, b *bot.Bot, dir string, batch []Inbound) string {
	parts := groupAlbums(batch)
	if len(parts) == 1 {
		return partPrompt(ctx, b, dir, parts[0])
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "The user sent %d messages in a row. Treat them as one request and answer them together.\n", len(parts))
	for i, part := range parts {
		fmt.Fprintf(&sb, "\n--- Message %d ---\n%s\n", i+1, partPrompt(ctx, b, dir, part))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func groupAlbums(batch []Inbound) [][]Inbound {
	var parts [][]Inbound
	for _, in := range batch {
		last := len(parts) - 1
		if last >= 0 && in.mediaGroup != "" && parts[last][0].mediaGroup == in.mediaGroup {
			parts[last] = append(parts[last], in)
			continue
		}
		parts = append(parts, []Inbound{in})
	}
	return parts
}

func partPrompt(ctx context.Context, b *bot.Bot, dir string, part []Inbound) string {
	if len(part) == 1 {
		return buildPrompt(ctx, b, part[0].text, part[0].att, dir)
	}
	return albumPrompt(ctx, b, dir, part)
}

func albumPrompt(ctx context.Context, b *bot.Bot, dir string, part []Inbound) string {
	var files, failed, texts []string
	for _, in := range part {
		if in.text != "" {
			texts = append(texts, in.text)
		}
		if in.att == nil {
			continue
		}
		path, err := downloadAttachment(ctx, b, in.att, dir)
		if err != nil {
			log.Printf("handler: download error: %v", err)
			failed = append(failed, fmt.Sprintf("%s (%v)", in.att.filename, err))
			continue
		}
		files = append(files, fmt.Sprintf("%s (saved at %s)", in.att.filename, path))
	}
	return joinNonEmpty(filesSentence(files), failedSentence(failed), strings.Join(texts, "\n"))
}

func filesSentence(files []string) string {
	if len(files) == 0 {
		return ""
	}
	return fmt.Sprintf("I'm sending you %d files: %s. Please read them.", len(files), strings.Join(files, ", "))
}

func failedSentence(failed []string) string {
	if len(failed) == 0 {
		return ""
	}
	return "(these attachments failed to download: " + strings.Join(failed, ", ") + ")"
}

func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}
