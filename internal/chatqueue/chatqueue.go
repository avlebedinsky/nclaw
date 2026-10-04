// Package chatqueue serializes work per chat/thread in strict arrival order. Consecutive
// user messages waiting in a lane are handed over as one batch, and every lane can be
// stopped or inspected while it runs.
package chatqueue

import (
	"context"
	"errors"
	"log"
	"slices"
	"sync"
	"time"
)

// Key identifies a chat lane.
type Key struct {
	ChatID   int64
	ThreadID int
}

// Kind classifies queued work.
type Kind int

// Work kinds.
const (
	KindUser Kind = iota
	KindScheduled
	KindWebhook
	KindControl
)

// Errors reported by the queue.
var (
	ErrStopped      = errors.New("stopped by user")
	ErrInterrupted  = errors.New("interrupted to answer newer messages")
	ErrShuttingDown = errors.New("shutting down")
	ErrDropped      = errors.New("dropped from queue")
)

// Job is a unit of non-message work run in a chat's lane.
type Job struct {
	Kind  Kind
	Label string
	Fn    func(ctx context.Context)
}

// BatchFunc handles a batch of consecutive user messages of one lane.
type BatchFunc[M any] func(ctx context.Context, key Key, batch []M)

// Options tunes a Queue.
type Options struct {
	MaxSettle time.Duration
}

// StopResult describes what Stop interrupted.
type StopResult struct {
	Canceled    bool
	Kind        Kind
	Label       string
	Elapsed     time.Duration
	DroppedUser int
	DroppedJobs int
}

// Snapshot describes a lane's current state.
type Snapshot struct {
	Running     bool
	Kind        Kind
	Label       string
	Started     time.Time
	Batch       int
	PendingUser int
	PendingJobs int
}

// Interrupted describes a lane affected by Close.
type Interrupted struct {
	Key         Key
	Running     bool
	Kind        Kind
	DroppedUser int
	DroppedJobs int
}

type entry[M any] struct {
	kind  Kind
	label string
	msg   M
	fn    func(context.Context)
	done  chan error
}

func (e entry[M]) isUser() bool {
	return e.fn == nil
}

type running struct {
	kind    Kind
	label   string
	batch   int
	started time.Time
	cancel  context.CancelCauseFunc
}

type lane[M any] struct {
	pending    []entry[M]
	current    *running
	quietUntil time.Time
	firstUser  time.Time
	wake       chan struct{}
}

// Queue runs work per chat lane, one item at a time, in arrival order.
type Queue[M any] struct {
	run    BatchFunc[M]
	opts   Options
	mu     sync.Mutex
	lanes  map[Key]*lane[M]
	closed bool
	wg     sync.WaitGroup
}

// New creates a Queue that hands user batches to run.
func New[M any](run BatchFunc[M], opts Options) *Queue[M] {
	if opts.MaxSettle <= 0 {
		opts.MaxSettle = 5 * time.Second
	}
	return &Queue[M]{run: run, opts: opts, lanes: make(map[Key]*lane[M])}
}

// Enqueue adds a user message to its lane. The lane waits until no new message has
// arrived for settle (bounded by MaxSettle) before starting a batch.
func (q *Queue[M]) Enqueue(key Key, msg M, settle time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrShuttingDown
	}

	ln := q.laneLocked(key)
	now := time.Now()
	if ln.firstUser.IsZero() {
		ln.firstUser = now
	}
	quiet := now.Add(settle)
	if limit := ln.firstUser.Add(q.opts.MaxSettle); quiet.After(limit) {
		quiet = limit
	}
	if quiet.After(ln.quietUntil) {
		ln.quietUntil = quiet
	}
	ln.pending = append(ln.pending, entry[M]{kind: KindUser, msg: msg})
	poke(ln)
	return nil
}

// Submit adds a job to its lane and returns a channel that receives nil once the job
// has run, or ErrDropped / ErrShuttingDown if it never will.
func (q *Queue[M]) Submit(key Key, job Job) (<-chan error, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, ErrShuttingDown
	}

	done := make(chan error, 1)
	ln := q.laneLocked(key)
	ln.pending = append(ln.pending, entry[M]{kind: job.Kind, label: job.Label, fn: job.Fn, done: done})
	poke(ln)
	return done, nil
}

// Do submits a job and waits until it has run or was dropped.
func (q *Queue[M]) Do(key Key, job Job) error {
	done, err := q.Submit(key, job)
	if err != nil {
		return err
	}
	return <-done
}

// Stop drops everything waiting in the lane and cancels the running item with ErrStopped.
func (q *Queue[M]) Stop(key Key) StopResult {
	q.mu.Lock()
	defer q.mu.Unlock()

	var res StopResult
	ln := q.lanes[key]
	if ln == nil {
		return res
	}
	res.DroppedUser, res.DroppedJobs = dropPending(ln, ErrDropped)
	if c := ln.current; c != nil {
		res.Canceled, res.Kind, res.Label, res.Elapsed = true, c.kind, c.label, time.Since(c.started)
		c.cancel(ErrStopped)
	}
	poke(ln)
	return res
}

// Interrupt cancels the running batch of user messages with ErrInterrupted and keeps
// what is waiting in the lane, so the next batch starts once it ends. It reports whether
// a user batch was running.
func (q *Queue[M]) Interrupt(key Key) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	ln := q.lanes[key]
	if ln == nil || ln.current == nil || ln.current.kind != KindUser {
		return false
	}
	ln.current.cancel(ErrInterrupted)
	return true
}

// Snapshot reports what the lane is doing.
func (q *Queue[M]) Snapshot(key Key) Snapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	var snap Snapshot
	ln := q.lanes[key]
	if ln == nil {
		return snap
	}
	for _, e := range ln.pending {
		if e.isUser() {
			snap.PendingUser++
		} else {
			snap.PendingJobs++
		}
	}
	if c := ln.current; c != nil {
		snap.Running, snap.Kind, snap.Label, snap.Started, snap.Batch = true, c.kind, c.label, c.started, c.batch
	}
	return snap
}

// Close refuses new work, drops everything pending and cancels running items with
// ErrShuttingDown. It returns the lanes that lost work; call Wait to let them finish.
func (q *Queue[M]) Close() []Interrupted {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true

	var report []Interrupted
	for key, ln := range q.lanes {
		item := Interrupted{Key: key}
		item.DroppedUser, item.DroppedJobs = dropPending(ln, ErrShuttingDown)
		if c := ln.current; c != nil {
			item.Running, item.Kind = true, c.kind
			c.cancel(ErrShuttingDown)
		}
		if item.Running || item.DroppedUser > 0 || item.DroppedJobs > 0 {
			report = append(report, item)
		}
		poke(ln)
	}
	return report
}

// Wait blocks until every lane worker has exited or ctx is done.
func (q *Queue[M]) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *Queue[M]) laneLocked(key Key) *lane[M] {
	ln := q.lanes[key]
	if ln == nil {
		ln = &lane[M]{wake: make(chan struct{}, 1)}
		q.lanes[key] = ln
		q.wg.Add(1)
		go q.work(key, ln)
	}
	return ln
}

func dropPending[M any](ln *lane[M], reason error) (users, jobs int) {
	for _, e := range ln.pending {
		if e.isUser() {
			users++
			continue
		}
		jobs++
		e.done <- reason
	}
	ln.pending = nil
	ln.firstUser, ln.quietUntil = time.Time{}, time.Time{}
	return users, jobs
}

func poke[M any](ln *lane[M]) {
	select {
	case ln.wake <- struct{}{}:
	default:
	}
}

type step[M any] struct {
	exit  bool
	wait  time.Duration
	batch []entry[M]
	ctx   context.Context
}

func (q *Queue[M]) work(key Key, ln *lane[M]) {
	defer q.wg.Done()
	for {
		st := q.next(key, ln)
		switch {
		case st.exit:
			return
		case st.batch == nil:
			sleep(ln, st.wait)
		default:
			q.exec(key, ln, st)
		}
	}
}

func (q *Queue[M]) next(key Key, ln *lane[M]) step[M] {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(ln.pending) == 0 {
		delete(q.lanes, key)
		return step[M]{exit: true}
	}
	head := ln.pending[0]
	if head.isUser() {
		if d := time.Until(ln.quietUntil); d > 0 {
			return step[M]{wait: d}
		}
	}

	n := 1
	for head.isUser() && n < len(ln.pending) && ln.pending[n].isUser() {
		n++
	}
	batch := slices.Clone(ln.pending[:n])
	ln.pending = ln.pending[n:]
	if head.isUser() {
		ln.firstUser, ln.quietUntil = time.Time{}, time.Time{}
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	ln.current = &running{kind: head.kind, label: head.label, batch: n, started: time.Now(), cancel: cancel}
	return step[M]{batch: batch, ctx: ctx}
}

func sleep[M any](ln *lane[M], d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ln.wake:
	}
}

func (q *Queue[M]) exec(key Key, ln *lane[M], st step[M]) {
	err := q.safeRun(key, st)

	q.mu.Lock()
	ln.current.cancel(nil)
	ln.current = nil
	q.mu.Unlock()

	for _, e := range st.batch {
		if e.done != nil {
			e.done <- err
		}
	}
}

func (q *Queue[M]) safeRun(key Key, st step[M]) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("chatqueue: chat=%d thread=%d panic: %v", key.ChatID, key.ThreadID, r)
			err = errors.New("chatqueue: work panicked")
		}
	}()

	if !st.batch[0].isUser() {
		st.batch[0].fn(st.ctx)
		return nil
	}
	msgs := make([]M, len(st.batch))
	for i, e := range st.batch {
		msgs[i] = e.msg
	}
	q.run(st.ctx, key, msgs)
	return nil
}
