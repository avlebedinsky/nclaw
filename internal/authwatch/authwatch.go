// Package authwatch warns ahead of time that the CLI backend's stored sign-in is
// about to expire, so it can be renewed before the bot starts failing.
package authwatch

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	day             = 24 * time.Hour
	defaultInterval = 6 * time.Hour
)

var thresholds = []time.Duration{5 * day, 2 * day, day, 0}

// Options configures a Watcher.
type Options struct {
	Name      string
	Expiry    func() (expiry time.Time, ok bool)
	Notify    func(ctx context.Context, text string) error
	RenewHint string
	Location  *time.Location
	Interval  time.Duration
	Now       func() time.Time
}

// Watcher checks the sign-in expiry periodically and warns once per threshold:
// 5, 2 and 1 day before it, and when it has passed. Signing in again resets it.
type Watcher struct {
	opts   Options
	mu     sync.Mutex
	expiry time.Time
	level  int
}

// New creates a Watcher.
func New(opts Options) *Watcher {
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Watcher{opts: opts}
}

// Run checks right away and then every interval until ctx is done.
func (w *Watcher) Run(ctx context.Context) {
	w.Check(ctx)
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Check(ctx)
		}
	}
}

// Check sends a warning if the sign-in has crossed a threshold not yet reported.
func (w *Watcher) Check(ctx context.Context) {
	expiry, ok := w.opts.Expiry()
	if !ok {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if !expiry.Equal(w.expiry) {
		w.expiry, w.level = expiry, 0
	}
	now := w.opts.Now()
	level := levelAt(expiry.Sub(now))
	if level <= w.level {
		return
	}
	if err := w.opts.Notify(ctx, w.warning(expiry, now)); err != nil {
		log.Printf("authwatch: notify: %v", err)
		return
	}
	w.level = level
}

// Status describes the sign-in's expiry in one line, or returns "" when it is unknown.
func (w *Watcher) Status() string {
	expiry, ok := w.opts.Expiry()
	if !ok {
		return ""
	}
	now := w.opts.Now()
	if !expiry.After(now) {
		return fmt.Sprintf("%s sign-in: expired on %s.", w.opts.Name, w.when(expiry))
	}
	return fmt.Sprintf("%s sign-in: valid until %s (%s left).", w.opts.Name, w.when(expiry), humanDuration(expiry.Sub(now)))
}

func (w *Watcher) warning(expiry, now time.Time) string {
	if !expiry.After(now) {
		return fmt.Sprintf("⛔ The bot's %s sign-in expired on %s, so its replies will fail. %s",
			w.opts.Name, w.when(expiry), w.opts.RenewHint)
	}
	return fmt.Sprintf("⚠️ The bot's %s sign-in expires in %s (%s). %s",
		w.opts.Name, humanDuration(expiry.Sub(now)), w.when(expiry), w.opts.RenewHint)
}

func (w *Watcher) when(t time.Time) string {
	return t.In(w.opts.Location).Format("Mon 2 Jan 15:04 MST")
}

func levelAt(remaining time.Duration) int {
	level := 0
	for _, threshold := range thresholds {
		if remaining <= threshold {
			level++
		}
	}
	return level
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 2*day:
		return fmt.Sprintf("%d days", int(d/day))
	case d >= day:
		return "1 day"
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	case d >= time.Hour:
		return "1 hour"
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	default:
		return "1 minute"
	}
}
