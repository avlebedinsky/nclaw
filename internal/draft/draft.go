// Package draft streams the answer being generated into a Telegram message draft,
// the live preview Telegram shows in private chats.
package draft

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nickalie/nclaw/internal/blocks"
	"github.com/nickalie/nclaw/internal/telegram"
)

const maxDraftRunes = 4096

var lastID atomic.Int64

// API sends a message draft; an empty text shows Telegram's "Thinking…" placeholder.
type API interface {
	SendDraft(ctx context.Context, chatID int64, threadID int, draftID int64, text string) error
}

// Options tunes a Drafter.
type Options struct {
	Interval  time.Duration
	KeepAlive time.Duration
}

// Drafter keeps one draft up to date with the text the agent is producing.
type Drafter struct {
	api      API
	chatID   int64
	threadID int
	id       int64
	opts     Options

	mu       sync.Mutex
	text     string
	sent     string
	sentAt   time.Time
	attempts int
	failed   bool

	quit chan struct{}
	done chan struct{}
}

// New starts a Drafter that immediately shows the "Thinking…" placeholder.
func New(api API, chatID int64, threadID int, opts Options) *Drafter {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = 20 * time.Second
	}
	d := &Drafter{
		api: api, chatID: chatID, threadID: threadID, id: lastID.Add(1), opts: opts,
		quit: make(chan struct{}), done: make(chan struct{}),
	}
	go d.loop()
	return d
}

// ID returns the draft identifier, which Telegram reports back when the user stops generation.
func (d *Drafter) ID() int64 {
	return d.id
}

// Update records the latest generated text; it never blocks on the network.
func (d *Drafter) Update(text string) {
	d.mu.Lock()
	d.text = text
	d.mu.Unlock()
}

// Finish stops updating the draft. It is safe on a nil Drafter.
func (d *Drafter) Finish() {
	if d == nil {
		return
	}
	close(d.quit)
	<-d.done
}

func (d *Drafter) loop() {
	defer close(d.done)
	d.flush()
	ticker := time.NewTicker(d.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-d.quit:
			return
		case <-ticker.C:
			d.flush()
		}
	}
}

func (d *Drafter) flush() {
	d.mu.Lock()
	text, due := d.pendingLocked(time.Now())
	d.mu.Unlock()
	if !due {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := d.api.SendDraft(ctx, d.chatID, d.threadID, d.id, text)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.attempts++
	d.sent, d.sentAt = text, time.Now()
	if err != nil {
		d.failed = true
		log.Printf("draft: chat=%d: %v; live drafts stop for this run", d.chatID, err)
	}
}

func (d *Drafter) pendingLocked(now time.Time) (string, bool) {
	if d.failed {
		return "", false
	}
	text := preview(d.text)
	if d.attempts == 0 {
		return text, true
	}
	return text, text != d.sent || now.Sub(d.sentAt) >= d.opts.KeepAlive
}

func preview(text string) string {
	plain := []rune(telegram.PlainText(blocks.StripPartial(text)))
	if len(plain) > maxDraftRunes {
		plain = append(plain[:maxDraftRunes-1], '…')
	}
	return string(plain)
}
