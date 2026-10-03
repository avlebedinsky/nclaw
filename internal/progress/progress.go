// Package progress shows what the agent is doing in a single Telegram message
// that is edited as tool calls stream in and removed when the run ends.
package progress

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/go-telegram/bot"

	"github.com/nickalie/nclaw/internal/cli"
)

var secretRe = regexp.MustCompile(
	`(?i)(token|secret|password|passwd|api[_-]?key|authorization)(\S*?)([=:]\s*|\s+)(?:(?:bearer|basic)\s+)?\S+`,
)

var icons = map[string]string{
	"Bash": "🔧", "Read": "📖", "Write": "✏️", "Edit": "✏️", "MultiEdit": "✏️", "NotebookEdit": "✏️",
	"Grep": "🔎", "Glob": "🔎", "WebFetch": "🌐", "WebSearch": "🌐", "Task": "🤖", "Agent": "🤖",
	"TodoWrite": "📝", "Skill": "🧩",
}

// MessageAPI is the slice of the Telegram API a Reporter needs.
type MessageAPI interface {
	Send(ctx context.Context, chatID int64, threadID int, text string) (int, error)
	Edit(ctx context.Context, chatID int64, msgID int, text string) error
	Delete(ctx context.Context, chatID int64, msgID int) error
}

// Options tunes a Reporter.
type Options struct {
	MinInterval time.Duration
	Heartbeat   time.Duration
	Tick        time.Duration
}

// Reporter keeps one status message per run up to date.
type Reporter struct {
	api      MessageAPI
	chatID   int64
	threadID int
	opts     Options

	mu          sync.Mutex
	step        string
	steps       int
	started     time.Time
	msgID       int
	lastStep    string
	lastFlush   time.Time
	nextAllowed time.Time

	kick chan struct{}
	done chan struct{}
	quit chan struct{}
}

// New starts a Reporter for a chat/thread. Call Finish when the run ends.
func New(api MessageAPI, chatID int64, threadID int, opts Options) *Reporter {
	if opts.MinInterval <= 0 {
		opts.MinInterval = 3 * time.Second
		if chatID < 0 {
			opts.MinInterval = 5 * time.Second
		}
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 30 * time.Second
	}
	if opts.Tick <= 0 {
		opts.Tick = time.Second
	}
	r := &Reporter{
		api: api, chatID: chatID, threadID: threadID, opts: opts, started: time.Now(),
		kick: make(chan struct{}, 1), done: make(chan struct{}), quit: make(chan struct{}),
	}
	go r.loop()
	return r
}

// OnTool records a tool call; it never blocks on the network.
func (r *Reporter) OnTool(ev cli.ToolEvent) {
	r.mu.Lock()
	r.step = render(ev)
	r.steps++
	r.mu.Unlock()

	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Finish stops updates and deletes the status message. It is safe on a nil Reporter.
func (r *Reporter) Finish(ctx context.Context) {
	if r == nil {
		return
	}
	close(r.quit)
	<-r.done

	r.mu.Lock()
	msgID := r.msgID
	r.mu.Unlock()
	if msgID == 0 {
		return
	}
	if err := r.api.Delete(ctx, r.chatID, msgID); err != nil {
		log.Printf("progress: delete status message: %v", err)
	}
}

func (r *Reporter) loop() {
	defer close(r.done)
	ticker := time.NewTicker(r.opts.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-r.quit:
			return
		case <-r.kick:
		case <-ticker.C:
		}
		r.flush()
	}
}

func (r *Reporter) flush() {
	r.mu.Lock()
	step := r.step
	text, msgID, due := r.pendingLocked(time.Now())
	r.mu.Unlock()
	if !due {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var err error
	if msgID == 0 {
		msgID, err = r.api.Send(ctx, r.chatID, r.threadID, text)
	} else {
		err = r.api.Edit(ctx, r.chatID, msgID, text)
	}
	r.record(step, msgID, err)
}

func (r *Reporter) pendingLocked(now time.Time) (text string, msgID int, due bool) {
	if r.steps == 0 || now.Before(r.nextAllowed) {
		return "", 0, false
	}
	text = fmt.Sprintf("%s\nstep %d · %s", r.step, r.steps, now.Sub(r.started).Round(time.Second))
	if r.msgID == 0 {
		return text, 0, true
	}
	since := now.Sub(r.lastFlush)
	if since < r.opts.MinInterval || (r.step == r.lastStep && since < r.opts.Heartbeat) {
		return "", 0, false
	}
	return text, r.msgID, true
}

func (r *Reporter) record(step string, msgID int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastFlush = time.Now()
	var tooMany *bot.TooManyRequestsError
	switch {
	case errors.As(err, &tooMany):
		r.nextAllowed = time.Now().Add(time.Duration(tooMany.RetryAfter) * time.Second)
	case err != nil:
		log.Printf("progress: update status message: %v", err)
	default:
		r.msgID, r.lastStep = msgID, step
	}
}

func render(ev cli.ToolEvent) string {
	icon, ok := icons[ev.Name]
	if !ok {
		icon = "⚙️"
	}
	line := icon + " " + ev.Name
	if ev.Detail != "" {
		line += ": " + secretRe.ReplaceAllString(ev.Detail, "$1$2$3***")
	}
	return line
}
