package chatqueue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const settle = 5 * time.Millisecond

type recorder struct {
	mu      sync.Mutex
	batches [][]string
	order   []string
	block   chan struct{}
	started chan struct{}
}

func newRecorder() *recorder {
	return &recorder{started: make(chan struct{}, 100)}
}

func (r *recorder) run(ctx context.Context, key Key, batch []string) {
	r.mu.Lock()
	r.batches = append(r.batches, batch)
	r.order = append(r.order, batch...)
	block := r.block
	r.mu.Unlock()
	r.started <- struct{}{}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
}

func (r *recorder) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.batches...)
}

func (r *recorder) record(label string) func(context.Context) {
	return func(context.Context) {
		r.mu.Lock()
		r.order = append(r.order, label)
		r.mu.Unlock()
	}
}

func waitStarted(t *testing.T, r *recorder) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(5 * time.Second):
		t.Fatal("batch did not start")
	}
}

func waitIdle(t *testing.T, q *Queue[string]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, q.Wait(ctx))
}

var key = Key{ChatID: 1, ThreadID: 2}

func TestEnqueue_BurstBecomesOneBatch(t *testing.T) {
	r := newRecorder()
	q := New(r.run, Options{})

	for _, m := range []string{"a", "b", "c"} {
		require.NoError(t, q.Enqueue(key, m, 50*time.Millisecond))
	}
	waitIdle(t, q)

	assert.Equal(t, [][]string{{"a", "b", "c"}}, r.snapshot())
}

func TestEnqueue_MessagesDuringRunAreMergedInOrder(t *testing.T) {
	r := newRecorder()
	r.block = make(chan struct{})
	q := New(r.run, Options{})

	require.NoError(t, q.Enqueue(key, "first", settle))
	waitStarted(t, r)
	for _, m := range []string{"second", "third"} {
		require.NoError(t, q.Enqueue(key, m, settle))
	}
	close(r.block)
	waitIdle(t, q)

	assert.Equal(t, [][]string{{"first"}, {"second", "third"}}, r.snapshot())
}

func TestEnqueue_SettleIsCappedByMaxSettle(t *testing.T) {
	r := newRecorder()
	q := New(r.run, Options{MaxSettle: 50 * time.Millisecond})
	start := time.Now()

	require.NoError(t, q.Enqueue(key, "a", time.Hour))
	waitStarted(t, r)

	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestLanesRunIndependently(t *testing.T) {
	r := newRecorder()
	r.block = make(chan struct{})
	q := New(r.run, Options{})

	require.NoError(t, q.Enqueue(Key{ChatID: 1}, "one", settle))
	require.NoError(t, q.Enqueue(Key{ChatID: 2}, "two", settle))
	waitStarted(t, r)
	waitStarted(t, r)
	close(r.block)
	waitIdle(t, q)

	assert.Len(t, r.snapshot(), 2)
}

func TestJobsKeepOrderWithMessages(t *testing.T) {
	r := newRecorder()
	r.block = make(chan struct{})
	q := New(r.run, Options{})

	require.NoError(t, q.Enqueue(key, "u1", settle))
	waitStarted(t, r)
	done, err := q.Submit(key, Job{Kind: KindScheduled, Fn: r.record("job")})
	require.NoError(t, err)
	require.NoError(t, q.Enqueue(key, "u2", settle))
	close(r.block)

	require.NoError(t, <-done)
	waitIdle(t, q)
	assert.Equal(t, []string{"u1", "job", "u2"}, r.order)
}

func TestDo_RunsJobAndReturnsNil(t *testing.T) {
	q := New(newRecorder().run, Options{})
	ran := false

	err := q.Do(key, Job{Kind: KindWebhook, Fn: func(context.Context) { ran = true }})

	require.NoError(t, err)
	assert.True(t, ran)
}

func TestStop_CancelsRunningAndDropsPending(t *testing.T) {
	r := newRecorder()
	r.block = make(chan struct{})
	q := New(r.run, Options{})

	var cause error
	jobStarted := make(chan struct{})
	jobDone, err := q.Submit(key, Job{Kind: KindScheduled, Label: "daily", Fn: func(ctx context.Context) {
		close(jobStarted)
		<-ctx.Done()
		cause = context.Cause(ctx)
	}})
	require.NoError(t, err)
	<-jobStarted
	require.NoError(t, q.Enqueue(key, "queued", settle))
	dropped, err := q.Submit(key, Job{Kind: KindWebhook, Fn: func(context.Context) { t.Error("dropped job ran") }})
	require.NoError(t, err)

	res := q.Stop(key)

	assert.True(t, res.Canceled)
	assert.Equal(t, KindScheduled, res.Kind)
	assert.Equal(t, "daily", res.Label)
	assert.Equal(t, 1, res.DroppedUser)
	assert.Equal(t, 1, res.DroppedJobs)
	assert.ErrorIs(t, <-dropped, ErrDropped)
	require.NoError(t, <-jobDone)
	assert.ErrorIs(t, cause, ErrStopped)
	waitIdle(t, q)
	assert.Empty(t, r.snapshot())
}

func TestStop_IdleLane(t *testing.T) {
	q := New(newRecorder().run, Options{})
	assert.Equal(t, StopResult{}, q.Stop(key))
}

func TestSnapshot(t *testing.T) {
	r := newRecorder()
	r.block = make(chan struct{})
	q := New(r.run, Options{})

	require.NoError(t, q.Enqueue(key, "a", settle))
	waitStarted(t, r)
	require.NoError(t, q.Enqueue(key, "b", settle))
	_, err := q.Submit(key, Job{Kind: KindScheduled, Fn: func(context.Context) {}})
	require.NoError(t, err)

	snap := q.Snapshot(key)
	close(r.block)
	waitIdle(t, q)

	assert.True(t, snap.Running)
	assert.Equal(t, KindUser, snap.Kind)
	assert.Equal(t, 1, snap.Batch)
	assert.Equal(t, 1, snap.PendingUser)
	assert.Equal(t, 1, snap.PendingJobs)
	assert.False(t, snap.Started.IsZero())
	assert.Equal(t, Snapshot{}, q.Snapshot(key))
}

func TestClose_CancelsAndRefusesNewWork(t *testing.T) {
	r := newRecorder()
	r.block = make(chan struct{})
	q := New(r.run, Options{})

	require.NoError(t, q.Enqueue(key, "running", settle))
	waitStarted(t, r)
	require.NoError(t, q.Enqueue(key, "pending", settle))
	pendingJob, err := q.Submit(key, Job{Kind: KindWebhook, Fn: func(context.Context) {}})
	require.NoError(t, err)

	report := q.Close()

	require.Len(t, report, 1)
	assert.Equal(t, Interrupted{Key: key, Running: true, Kind: KindUser, DroppedUser: 1, DroppedJobs: 1}, report[0])
	assert.ErrorIs(t, <-pendingJob, ErrShuttingDown)
	assert.ErrorIs(t, q.Enqueue(key, "late", settle), ErrShuttingDown)
	assert.ErrorIs(t, q.Do(key, Job{Fn: func(context.Context) {}}), ErrShuttingDown)
	waitIdle(t, q)
}

func TestWait_RespectsDeadline(t *testing.T) {
	r := newRecorder()
	r.block = make(chan struct{})
	defer close(r.block)
	q := New(r.run, Options{})
	require.NoError(t, q.Enqueue(key, "stuck", settle))
	waitStarted(t, r)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, q.Wait(ctx), context.DeadlineExceeded)
}

func TestPanicDoesNotKillLane(t *testing.T) {
	q := New(newRecorder().run, Options{})

	err := q.Do(key, Job{Fn: func(context.Context) { panic("boom") }})
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrDropped))

	assert.NoError(t, q.Do(key, Job{Fn: func(context.Context) {}}))
}

func TestIdleLanesAreRemoved(t *testing.T) {
	q := New(newRecorder().run, Options{})
	require.NoError(t, q.Do(key, Job{Fn: func(context.Context) {}}))
	waitIdle(t, q)

	q.mu.Lock()
	defer q.mu.Unlock()
	assert.Empty(t, q.lanes)
}
