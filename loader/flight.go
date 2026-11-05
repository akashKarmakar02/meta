package loader

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/akashKarmakar02/meta/cache"
)

// ErrFlightPanic reports a panic in a shared loader computation. Internal
// panic values are deliberately not exposed to waiters or HTTP clients.
var ErrFlightPanic = errors.New("loader: shared computation panicked")

// FlightGroup coordinates explicitly opted-in cache misses within one E04
// invalidation domain. It owns no store and starts no idle goroutines.
// Callers must use the same Invalidator for the group and their bindings.
type FlightGroup struct {
	invalidator *Invalidator
	timeout     time.Duration
	mu          sync.Mutex
	calls       map[flightID]*flightCall
}

type flightBinding struct{ marker byte }
type flightID struct {
	fence      *keyFence
	generation uint64
	binding    *flightBinding
}
type flightResult struct {
	value     any
	encoded   []byte
	shareable bool
	err       error
}
type flightCall struct {
	done     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	waiters  int
	finished bool
	claimed  bool
	result   flightResult
}

// NewFlightGroup creates a process-local coordinator. timeout bounds shared
// work and must be positive. Loaders and stores must cooperate with context
// cancellation; Go cannot forcibly stop a function that ignores cancellation.
func NewFlightGroup(invalidator *Invalidator, timeout time.Duration) (*FlightGroup, error) {
	if invalidator == nil || invalidator.fence == nil || nilStore(invalidator.store) || timeout <= 0 {
		return nil, fmt.Errorf("%w: flight group requires a valid invalidator and positive timeout", ErrInvalidCacheConfig)
	}
	return &FlightGroup{invalidator: invalidator, timeout: timeout, calls: make(map[flightID]*flightCall)}, nil
}

// wait registers a waiter against the exact live fence record, generation and
// binding identity. Lock order: group.mu -> fence map mutex only on creation;
// invalidation never takes group.mu. Neither lock is held across application
// callbacks, codecs, loader execution or store operations. Each flight holds a
// separate fence reference until its goroutine returns, preventing ABA reuse.
// Invalidation linearizes at its generation bump under keyFence.mu. A request
// whose beginFill happens afterwards necessarily uses a different flight ID.
func (g *FlightGroup) wait(ctx context.Context, key cache.Key, fence *keyFence, generation uint64, binding *flightBinding, emit func(CacheEvent), work func(context.Context) flightResult) (flightResult, bool, error) {
	id := flightID{fence: fence, generation: generation, binding: binding}
	g.mu.Lock()
	call := g.calls[id]
	event := CacheFlightJoined
	if call == nil {
		event = CacheFlightCreated
		// The requesting fill owns a reference while this is acquired, so it must
		// be the identical record. The flight keeps its own reference after the
		// first waiter departs.
		held := g.invalidator.fence.acquire(key)
		sharedCtx, cancel := context.WithTimeout(context.Background(), g.timeout)
		call = &flightCall{done: make(chan struct{}), ctx: sharedCtx, cancel: cancel}
		g.calls[id] = call
		go g.run(id, call, held, sharedCtx, work)
	}
	call.waiters++
	g.mu.Unlock()
	defer g.leave(id, call)
	emit(event)
	select {
	case <-ctx.Done():
		return flightResult{}, false, ctx.Err()
	case <-call.ctx.Done():
		select {
		case <-call.done:
		default:
			return flightResult{}, false, call.ctx.Err()
		}
	case <-call.done:
	}
	if err := ctx.Err(); err != nil {
		return flightResult{}, false, err
	}
	g.mu.Lock()
	result := call.result
	claimed := false
	if result.err == nil && !result.shareable && !call.claimed {
		call.claimed = true
		claimed = true
	}
	g.mu.Unlock()
	return result, claimed, nil
}

func (g *FlightGroup) run(id flightID, call *flightCall, held *keyFence, ctx context.Context, work func(context.Context) flightResult) {
	result := flightResult{}
	finishedNormally := false
	defer func() {
		panicValue := recover()
		if panicValue != nil || !finishedNormally {
			result = flightResult{err: ErrFlightPanic}
		}
		if err := ctx.Err(); err != nil {
			result = flightResult{err: err}
		}
		g.mu.Lock()
		call.result = result
		call.finished = true
		if g.calls[id] == call {
			delete(g.calls, id)
		}
		close(call.done)
		g.mu.Unlock()
		call.cancel()
		g.invalidator.fence.release(held)
	}()
	result = work(ctx)
	finishedNormally = true
}

func (g *FlightGroup) leave(id flightID, call *flightCall) {
	g.mu.Lock()
	call.waiters--
	if call.waiters == 0 && !call.finished {
		if g.calls[id] == call {
			delete(g.calls, id)
		}
		call.cancel()
	}
	g.mu.Unlock()
}
