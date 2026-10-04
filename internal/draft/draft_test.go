package draft

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAPI struct {
	mu    sync.Mutex
	texts []string
	ids   []int64
	err   error
}

func (f *fakeAPI) SendDraft(_ context.Context, _ int64, _ int, id int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	f.ids = append(f.ids, id)
	return f.err
}

func (f *fakeAPI) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

func fast() Options {
	return Options{Interval: 5 * time.Millisecond, KeepAlive: time.Hour}
}

func TestDrafter_ShowsThinkingThenText(t *testing.T) {
	api := &fakeAPI{}
	d := New(api, 1, 0, fast())

	require.Eventually(t, func() bool { return len(api.snapshot()) == 1 }, time.Second, time.Millisecond)
	d.Update("<b>Hel</b>")
	require.Eventually(t, func() bool { return len(api.snapshot()) == 2 }, time.Second, time.Millisecond)
	d.Update("<b>Hello</b> &amp; bye")
	require.Eventually(t, func() bool { return len(api.snapshot()) == 3 }, time.Second, time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	d.Finish()

	assert.Equal(t, []string{"", "Hel", "Hello & bye"}, api.snapshot())
	for _, id := range api.ids {
		assert.Equal(t, d.ID(), id)
	}
}

func TestDrafter_KeepAliveResendsUnchangedText(t *testing.T) {
	api := &fakeAPI{}
	d := New(api, 1, 0, Options{Interval: 5 * time.Millisecond, KeepAlive: 20 * time.Millisecond})
	d.Update("same")

	require.Eventually(t, func() bool { return len(api.snapshot()) >= 4 }, time.Second, time.Millisecond)
	d.Finish()

	assert.Equal(t, "same", api.snapshot()[3])
}

func TestDrafter_StopsAfterError(t *testing.T) {
	api := &fakeAPI{err: errors.New("Bad Request: draft_id must be an integer")}
	d := New(api, 1, 0, fast())
	require.Eventually(t, func() bool { return len(api.snapshot()) == 1 }, time.Second, time.Millisecond)

	d.Update("more text")
	time.Sleep(40 * time.Millisecond)
	d.Finish()

	assert.Len(t, api.snapshot(), 1)
}

func TestDrafter_TruncatesLongText(t *testing.T) {
	api := &fakeAPI{}
	d := New(api, 1, 0, fast())
	d.Update(strings.Repeat("я", 5000))

	require.Eventually(t, func() bool {
		texts := api.snapshot()
		return len(texts) > 0 && texts[len(texts)-1] != ""
	}, time.Second, time.Millisecond)
	d.Finish()

	texts := api.snapshot()
	last := []rune(texts[len(texts)-1])
	assert.Len(t, last, maxDraftRunes)
	assert.Equal(t, '…', last[len(last)-1])
}

func TestDrafter_UniqueIDsAndNilFinish(t *testing.T) {
	a, b := New(&fakeAPI{}, 1, 0, fast()), New(&fakeAPI{}, 1, 0, fast())
	defer a.Finish()
	defer b.Finish()
	assert.NotEqual(t, a.ID(), b.ID())
	assert.NotZero(t, a.ID())

	var d *Drafter
	assert.NotPanics(t, d.Finish)
}

func TestPreview_HidesCommandBlocksBeingWritten(t *testing.T) {
	assert.Equal(t, "Удалить?", preview("Удалить?\n```nclaw:buttons\n[\"Да\""))
	assert.Equal(t, "Готово", preview("Готово\n```nclaw:schedule\n{\"action\":\"create\"}\n```"))
}
