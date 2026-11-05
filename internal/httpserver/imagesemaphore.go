package httpserver

import (
	"context"
	"time"
)

// semaphore bounds concurrent native image transformations (F03 §27,
// F04 §24). It is deliberately not a general task scheduler: acquire
// waits a bounded time or honors cancellation, release runs deferred on
// every exit path, and no lock is held while user work runs. Tokens are
// buffered-channel sends, so a release can never block and there is no
// parked-goroutine residue.
type semaphore struct {
	tokens chan struct{}
}

func newSemaphore(slots int) *semaphore {
	if slots < 1 {
		slots = 1
	}
	s := &semaphore{tokens: make(chan struct{}, slots)}
	for i := 0; i < slots; i++ {
		s.tokens <- struct{}{}
	}
	return s
}

// maxPermitWait bounds how long a request may queue for a processing
// permit (F04 §25). Beyond it the endpoint rejects with 503 (overload)
// rather than letting requests pile up holding reader goroutines.
// Healthy traffic recovers as slots free up; the bound is per acquire.
const maxPermitWait = 10 * time.Second

// permitTimeoutError reports the bounded-wait overload condition.
type permitTimeoutError struct{}

func (permitTimeoutError) Error() string { return "image processing queue timeout" }

// errPermitTimeout reports the bounded-wait overload condition for the
// endpoint's status mapping.
var errPermitTimeout error = permitTimeoutError{}

// acquire takes a slot, blocking while none is free for at most
// maxPermitWait. Cancellation while waiting returns ctx.Err(); an
// expired wait returns errPermitTimeout for the caller to map to 503.
func (s *semaphore) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.tokens:
		return nil
	default:
	}
	timer := time.NewTimer(maxPermitWait)
	defer timer.Stop()
	select {
	case <-s.tokens:
		return nil
	case <-timer.C:
		return errPermitTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release returns a slot. Deferred by callers, so it runs on success,
// failure, and panic unwinding. Never blocks: the buffer always has room
// because acquire consumed exactly one token per in-flight request.
func (s *semaphore) release() {
	select {
	case s.tokens <- struct{}{}:
	default:
		// Cannot happen with correct pairing; fail safe rather than block.
	}
}
