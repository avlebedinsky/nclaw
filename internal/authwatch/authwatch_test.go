package authwatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	now    time.Time
	expiry time.Time
	known  bool
	sent   []string
	fail   error
}

func newWatcher(f *fixture) *Watcher {
	return New(Options{
		Name:      "Claude",
		Expiry:    func() (time.Time, bool) { return f.expiry, f.known },
		Notify:    func(_ context.Context, text string) error { f.sent = append(f.sent, text); return f.fail },
		RenewHint: "Send /login.",
		Location:  time.UTC,
		Now:       func() time.Time { return f.now },
	})
}

func newFixture() *fixture {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	return &fixture{now: now, expiry: now.Add(6 * day), known: true}
}

func TestCheck_WarnsOncePerThreshold(t *testing.T) {
	f := newFixture()
	w := newWatcher(f)
	ctx := context.Background()

	w.Check(ctx)
	assert.Empty(t, f.sent)

	f.now = f.now.Add(25 * time.Hour)
	w.Check(ctx)
	w.Check(ctx)
	require.Len(t, f.sent, 1)
	assert.Equal(t, "⚠️ The bot's Claude sign-in expires in 4 days (Fri 9 Oct 12:00 UTC). Send /login.", f.sent[0])

	f.now = f.expiry.Add(-20 * time.Hour)
	w.Check(ctx)
	require.Len(t, f.sent, 2)
	assert.Contains(t, f.sent[1], "expires in 20 hours")

	f.now = f.expiry.Add(time.Minute)
	w.Check(ctx)
	w.Check(ctx)
	require.Len(t, f.sent, 3)
	assert.Equal(t, "⛔ The bot's Claude sign-in expired on Fri 9 Oct 12:00 UTC, so its replies will fail. Send /login.", f.sent[2])
}

func TestCheck_SkipsLevelsAfterLongPause(t *testing.T) {
	f := newFixture()
	w := newWatcher(f)

	f.now = f.expiry.Add(-time.Hour)
	w.Check(context.Background())
	w.Check(context.Background())

	require.Len(t, f.sent, 1)
	assert.Contains(t, f.sent[0], "expires in 1 hour")
}

func TestCheck_NewSignInResetsWarnings(t *testing.T) {
	f := newFixture()
	w := newWatcher(f)
	f.now = f.expiry.Add(-time.Hour)
	w.Check(context.Background())

	f.expiry = f.now.Add(30 * day)
	w.Check(context.Background())
	f.now = f.expiry.Add(-3 * day)
	w.Check(context.Background())

	require.Len(t, f.sent, 2)
	assert.Contains(t, f.sent[1], "expires in 3 days")
}

func TestCheck_RetriesAfterFailedNotify(t *testing.T) {
	f := newFixture()
	f.now = f.expiry.Add(-3 * day)
	f.fail = errors.New("network down")
	w := newWatcher(f)

	w.Check(context.Background())
	f.fail = nil
	w.Check(context.Background())
	w.Check(context.Background())

	assert.Len(t, f.sent, 2)
}

func TestCheck_UnknownExpiryIsSilent(t *testing.T) {
	f := newFixture()
	f.known = false
	f.now = f.expiry.Add(time.Hour)
	w := newWatcher(f)

	w.Check(context.Background())

	assert.Empty(t, f.sent)
	assert.Empty(t, w.Status())
}

func TestStatus(t *testing.T) {
	f := newFixture()
	w := newWatcher(f)
	assert.Equal(t, "Claude sign-in: valid until Fri 9 Oct 12:00 UTC (6 days left).", w.Status())

	f.now = f.expiry.Add(time.Hour)
	assert.Equal(t, "Claude sign-in: expired on Fri 9 Oct 12:00 UTC.", w.Status())
}

func TestRun_ChecksAtStartAndStopsWithContext(t *testing.T) {
	f := newFixture()
	f.now = f.expiry.Add(-time.Hour)
	w := newWatcher(f)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.level > 0
	}, time.Second, 10*time.Millisecond)
	cancel()
	<-done
	assert.Len(t, f.sent, 1)
}

func TestHumanDuration(t *testing.T) {
	assert.Equal(t, "5 days", humanDuration(5*day+3*time.Hour))
	assert.Equal(t, "1 day", humanDuration(day+time.Hour))
	assert.Equal(t, "23 hours", humanDuration(23*time.Hour+59*time.Minute))
	assert.Equal(t, "1 hour", humanDuration(90*time.Minute))
	assert.Equal(t, "5 minutes", humanDuration(5*time.Minute+30*time.Second))
	assert.Equal(t, "1 minute", humanDuration(10*time.Second))
}
