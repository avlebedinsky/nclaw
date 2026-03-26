package telegram

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewChatLocker(t *testing.T) {
	cl := NewChatLocker()
	assert.NotNil(t, cl)
}

func TestLock_BasicLockUnlock(t *testing.T) {
	cl := NewChatLocker()
	unlock := cl.Lock(1, 0)
	assert.NotNil(t, unlock)
	unlock()
}

func TestLock_SameChatThreadSerializes(t *testing.T) {
	cl := NewChatLocker()
	var seq []int
	var mu sync.Mutex

	unlock := cl.Lock(1, 0)

	done := make(chan struct{})
	go func() {
		unlock2 := cl.Lock(1, 0)
		mu.Lock()
		seq = append(seq, 2)
		mu.Unlock()
		unlock2()
		close(done)
	}()

	// Give the goroutine time to block on the lock.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	seq = append(seq, 1)
	mu.Unlock()
	unlock()

	<-done

	assert.Equal(t, []int{1, 2}, seq)
}

func TestLock_DifferentChatsDontBlock(t *testing.T) {
	cl := NewChatLocker()
	var running atomic.Int32

	var wg sync.WaitGroup
	for i := int64(0); i < 5; i++ {
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			unlock := cl.Lock(chatID, 0)
			running.Add(1)
			// Hold the lock briefly so multiple goroutines overlap.
			time.Sleep(50 * time.Millisecond)
			running.Add(-1)
			unlock()
		}(i)
	}

	// Wait a bit then check that multiple goroutines ran concurrently.
	time.Sleep(30 * time.Millisecond)
	assert.Greater(t, int(running.Load()), 1, "different chats should run concurrently")

	wg.Wait()
}

func TestLock_DifferentThreadsSeparate(t *testing.T) {
	cl := NewChatLocker()

	unlock1 := cl.Lock(1, 10)

	acquired := make(chan struct{})
	go func() {
		unlock2 := cl.Lock(1, 20)
		close(acquired)
		unlock2()
	}()

	select {
	case <-acquired:
		// Different thread acquired without blocking — correct.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("different thread should not be blocked by another thread in same chat")
	}

	unlock1()
}
