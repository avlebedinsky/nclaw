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
	assert.Equal(t, "⚠️ Вход бота в Claude истекает через 4 дня (пт 9 окт 12:00 UTC). Send /login.", f.sent[0])

	f.now = f.expiry.Add(-20 * time.Hour)
	w.Check(ctx)
	require.Len(t, f.sent, 2)
	assert.Contains(t, f.sent[1], "истекает через 20 часов")

	f.now = f.expiry.Add(time.Minute)
	w.Check(ctx)
	w.Check(ctx)
	require.Len(t, f.sent, 3)
	assert.Equal(t, "⛔ Вход бота в Claude истёк пт 9 окт 12:00 UTC, ответы будут приходить с ошибкой. Send /login.", f.sent[2])
}

func TestCheck_SkipsLevelsAfterLongPause(t *testing.T) {
	f := newFixture()
	w := newWatcher(f)

	f.now = f.expiry.Add(-time.Hour)
	w.Check(context.Background())
	w.Check(context.Background())

	require.Len(t, f.sent, 1)
	assert.Contains(t, f.sent[0], "истекает через 1 час")
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
	assert.Contains(t, f.sent[1], "истекает через 3 дня")
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
	assert.Equal(t, "Вход в Claude действует до пт 9 окт 12:00 UTC (осталось 6 дней).", w.Status())

	f.now = f.expiry.Add(time.Hour)
	assert.Equal(t, "Вход в Claude истёк пт 9 окт 12:00 UTC.", w.Status())
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
	assert.Equal(t, "5 дней", humanDuration(5*day+3*time.Hour))
	assert.Equal(t, "1 день", humanDuration(day+time.Hour))
	assert.Equal(t, "23 часа", humanDuration(23*time.Hour+59*time.Minute))
	assert.Equal(t, "1 час", humanDuration(90*time.Minute))
	assert.Equal(t, "5 мин", humanDuration(5*time.Minute+30*time.Second))
	assert.Equal(t, "1 мин", humanDuration(10*time.Second))
}
