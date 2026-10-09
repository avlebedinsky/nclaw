package progress

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/cli"
)

type call struct {
	op    string
	msgID int
	text  string
	kb    buttons.Keyboard
}

type fakeAPI struct {
	mu      sync.Mutex
	calls   []call
	editErr error
}

func (f *fakeAPI) Send(_ context.Context, _ int64, _ int, text string, kb buttons.Keyboard) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{op: "send", msgID: 7, text: text, kb: kb})
	return 7, nil
}

func (f *fakeAPI) Edit(_ context.Context, _ int64, msgID int, text string, kb buttons.Keyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{op: "edit", msgID: msgID, text: text, kb: kb})
	return f.editErr
}

func (f *fakeAPI) Delete(_ context.Context, _ int64, msgID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{op: "delete", msgID: msgID})
	return nil
}

func (f *fakeAPI) snapshot() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func (f *fakeAPI) count(op string) int {
	n := 0
	for _, c := range f.snapshot() {
		if c.op == op {
			n++
		}
	}
	return n
}

func fastOptions() Options {
	return Options{MinInterval: 50 * time.Millisecond, Heartbeat: time.Hour, Tick: 5 * time.Millisecond}
}

func TestReporter_FirstToolSendsImmediately(t *testing.T) {
	api := &fakeAPI{}
	r := New(api, 1, 0, Options{MinInterval: time.Hour, Heartbeat: time.Hour, Tick: time.Hour})

	r.OnTool(cli.ToolEvent{Name: "Bash", Detail: "npm test"})

	require.Eventually(t, func() bool { return api.count("send") == 1 }, 2*time.Second, 5*time.Millisecond)
	assert.Contains(t, api.snapshot()[0].text, "🔧 Bash: npm test\nшаг 1 · ")
	r.Finish(context.Background())
	assert.Equal(t, call{op: "delete", msgID: 7}, api.snapshot()[1])
}

func TestReporter_ThrottlesBurstToLatestStep(t *testing.T) {
	api := &fakeAPI{}
	r := New(api, 1, 0, fastOptions())

	r.OnTool(cli.ToolEvent{Name: "Read", Detail: "a.go"})
	require.Eventually(t, func() bool { return api.count("send") == 1 }, 2*time.Second, 5*time.Millisecond)
	for _, f := range []string{"b.go", "c.go", "d.go", "e.go"} {
		r.OnTool(cli.ToolEvent{Name: "Read", Detail: f})
	}
	require.Eventually(t, func() bool {
		calls := api.snapshot()
		last := calls[len(calls)-1]
		return last.op == "edit" && last.msgID == 7 && len(last.text) > 0 && last.text[:len("📖 Read: e.go")] == "📖 Read: e.go"
	}, 2*time.Second, 5*time.Millisecond)
	r.Finish(context.Background())

	assert.LessOrEqual(t, api.count("edit"), 2)
}

func TestReporter_NoToolsNoMessage(t *testing.T) {
	api := &fakeAPI{}
	r := New(api, 1, 0, fastOptions())
	time.Sleep(30 * time.Millisecond)
	r.Finish(context.Background())

	assert.Empty(t, api.snapshot())
}

func TestReporter_SameStepIsNotReEdited(t *testing.T) {
	api := &fakeAPI{}
	r := New(api, 1, 0, fastOptions())
	r.OnTool(cli.ToolEvent{Name: "Bash", Detail: "make"})
	require.Eventually(t, func() bool { return api.count("send") == 1 }, 2*time.Second, 5*time.Millisecond)

	time.Sleep(150 * time.Millisecond)
	r.Finish(context.Background())

	assert.Zero(t, api.count("edit"))
}

func TestReporter_BacksOffOnTooManyRequests(t *testing.T) {
	api := &fakeAPI{editErr: &bot.TooManyRequestsError{Message: "slow down", RetryAfter: 3600}}
	r := New(api, 1, 0, fastOptions())
	r.OnTool(cli.ToolEvent{Name: "Bash", Detail: "one"})
	require.Eventually(t, func() bool { return api.count("send") == 1 }, 2*time.Second, 5*time.Millisecond)

	r.OnTool(cli.ToolEvent{Name: "Bash", Detail: "two"})
	require.Eventually(t, func() bool { return api.count("edit") == 1 }, 2*time.Second, 5*time.Millisecond)
	r.OnTool(cli.ToolEvent{Name: "Bash", Detail: "three"})
	time.Sleep(150 * time.Millisecond)
	r.Finish(context.Background())

	assert.Equal(t, 1, api.count("edit"))
}

func TestReporter_FinishNilSafe(t *testing.T) {
	var r *Reporter
	assert.NotPanics(t, func() { r.Finish(context.Background()) })
}

func TestRender(t *testing.T) {
	assert.Equal(t, "🔧 Bash: export API_KEY=***", render(cli.ToolEvent{Name: "Bash", Detail: "export API_KEY=sk-123"}))
	assert.Equal(t, "🔧 Bash: curl -H 'Authorization: ***", render(cli.ToolEvent{Name: "Bash", Detail: "curl -H 'Authorization: Bearer abc.def'"}))
	assert.Equal(t, "🔧 Bash: gh --token ***", render(cli.ToolEvent{Name: "Bash", Detail: "gh --token ghp_123"}))
	assert.Equal(t, "⚙️ github: create_issue", render(cli.ToolEvent{Name: "github", Detail: "create_issue"}))
	assert.Equal(t, "📝 TodoWrite", render(cli.ToolEvent{Name: "TodoWrite"}))
}

func TestNew_GroupChatsUseLongerInterval(t *testing.T) {
	r := New(&fakeAPI{}, -100, 0, Options{})
	defer r.Finish(context.Background())
	assert.Equal(t, 5*time.Second, r.opts.MinInterval)

	p := New(&fakeAPI{}, 100, 0, Options{})
	defer p.Finish(context.Background())
	assert.Equal(t, 3*time.Second, p.opts.MinInterval)
}

func TestReporter_ShowsControlsAndUpdatesThemWhenTheyChange(t *testing.T) {
	api := &fakeAPI{}
	var mu sync.Mutex
	pending := 0
	opts := fastOptions()
	opts.Controls = func() buttons.Keyboard {
		mu.Lock()
		defer mu.Unlock()
		return buttons.Progress(pending)
	}
	r := New(api, 1, 0, opts)

	r.OnTool(cli.ToolEvent{Name: "Bash", Detail: "make"})
	require.Eventually(t, func() bool { return api.count("send") == 1 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, buttons.Progress(0), api.snapshot()[0].kb)

	mu.Lock()
	pending = 2
	mu.Unlock()
	require.Eventually(t, func() bool { return api.count("edit") >= 1 }, 2*time.Second, 5*time.Millisecond)
	r.Finish(context.Background())

	calls := api.snapshot()
	assert.Equal(t, buttons.Progress(2), calls[1].kb)
	assert.Equal(t, 1, api.count("edit"))
}
