package httpserver

import (
	"context"
	"sync"
)

// semaphore bounds concurrent native image transformations (F03 §27). It
// is deliberately not a general task scheduler: acquire waits or honors
// cancellation, release runs deferred on every exit path, and no
// internal lock is held while user work runs.
type semaphore struct {
	mu    sync.Mutex
	cond  *sync.Cond
	slots int
	watch bool // a cancellation watcher goroutine has been started
}

func newSemaphore(slots int) *semaphore {
	if slots < 1 {
		slots = 1
	}
	s := &semaphore{slots: slots}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// acquire takes a slot, blocking while none is free. Cancellation while
// waiting returns ctx.Err() without holding a permit; one watcher
// goroutine (per semaphore) broadcasts context completion so waiters wake.
func (s *semaphore) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := ctx.Done()
	s.mu.Lock()
	if done != nil && !s.watch {
		s.watch = true
		s.mu.Unlock()
		go func() {
			<-done
			s.cond.Broadcast()
		}()
		s.mu.Lock()
	}
	for s.slots == 0 {
		s.cond.Wait()
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.slots--
	s.mu.Unlock()
	return nil
}

// release returns a slot. Deferred by callers, so it runs on success,
// failure, and panic unwinding.
func (s *semaphore) release() {
	s.mu.Lock()
	s.slots++
	s.cond.Signal()
	s.mu.Unlock()
}
