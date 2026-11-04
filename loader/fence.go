package loader

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go-meta/cache"
	"go-meta/routecontext"
)

// ErrStaleFill reports that a cache fill's write was suppressed because an
// invalidation completed while the fill was in flight. The fill's result is
// still returned to its original request; only the cache write was dropped.
var ErrStaleFill = errors.New("loader: stale fill suppressed")

// CacheFence coordinates cached loader fills with invalidations so a completed
// invalidation cannot be undone by an earlier in-flight fill (the race: a
// fill misses and starts a slow read; a mutation commits and invalidates; the
// fill finishes and its late Set repopulates the stale value).
//
// Mechanism: each key has a fence record with a generation counter. A fill
// takes a reference on the record and observes the generation before reading
// the store; immediately before writing it re-checks the generation WHILE
// HOLDING THE RECORD'S MUTEX and performs the Set inside that same critical
// section. An invalidation bumps the generation and deletes the store entry
// inside the same critical section (record mutex). The fill's check+write and
// the invalidation's advance+delete are therefore totally ordered: either the
// fill's write completes first and the invalidation's delete removes it, or
// the fill observes the advanced generation and suppresses its write. There is
// no interleaving between the two sections.
//
// Sharing: one fence belongs to one store (via an Invalidator). Every cached
// binding on that store must construct its cache options from that invalidator
// (CacheOptions.Invalidator) so all fills and invalidations share the fence;
// binding the same store without it leaves that binding's writes unfenced and
// therefore outside the stale-fill guarantee.
//
// Lifetime: records are reference-counted and exist only while fills (or an
// invalidation) hold them. The last release removes the record, so a
// long-lived server accumulates no per-key metadata; there is no background
// goroutine and no timer. A fill holds its reference across loader execution,
// so its record cannot be removed mid-fill; concurrent fills on distinct keys
// use distinct records and do not serialize.
//
// Locking: the package mutex guards only map membership (O(1)); each record's
// mutex is held solely around a generation check and a single store call —
// never across loader execution. A generation is uint64; wraparound requires
// 2^64 invalidations of one key, unreachable in practice.
type CacheFence struct {
	mu     sync.Mutex
	fences map[cache.Key]*keyFence
}

type keyFence struct {
	key  cache.Key
	mu   sync.Mutex
	gen  uint64
	refs int
}

func newCacheFence() *CacheFence {
	return &CacheFence{fences: make(map[cache.Key]*keyFence)}
}

// acquire takes a reference on key's record, creating it if absent.
func (f *CacheFence) acquire(key cache.Key) *keyFence {
	f.mu.Lock()
	defer f.mu.Unlock()
	kf := f.fences[key]
	if kf == nil {
		kf = &keyFence{key: key}
		f.fences[key] = kf
	}
	kf.refs++
	return kf
}

// release drops a reference; the last release removes the record. Callers
// never touch a record after release.
func (f *CacheFence) release(kf *keyFence) {
	f.mu.Lock()
	kf.refs--
	if kf.refs <= 0 {
		delete(f.fences, kf.key)
	}
	f.mu.Unlock()
}

// beginFill registers a fill and returns the record plus the generation the
// fill observes. The fill must hold the reference until it has completed (or
// abandoned) its write attempt.
func (f *CacheFence) beginFill(key cache.Key) (*keyFence, uint64) {
	kf := f.acquire(key)
	kf.mu.Lock()
	gen := kf.gen
	kf.mu.Unlock()
	return kf, gen
}

// storeIfCurrent performs the fill's cache write inside the record's critical
// section: if the observed generation is still current the Set runs within
// the section; otherwise nothing is written and ErrStaleFill is returned.
// Because the check and the Set are one section serialized against
// invalidate's advance+delete section, a write that passes the check cannot
// land after a completed invalidation.
func (f *CacheFence) storeIfCurrent(kf *keyFence, gen uint64, ctx context.Context, store cache.Store, key cache.Key, entry cache.Entry, policy cache.Policy) error {
	kf.mu.Lock()
	defer kf.mu.Unlock()
	if kf.gen != gen {
		return ErrStaleFill
	}
	return store.Set(ctx, key, entry, policy)
}

// deleteIfCurrent removes a corrupt entry only while the observed generation
// is current. A stale flight must never delete a newer generation's fill.
func (f *CacheFence) deleteIfCurrent(kf *keyFence, gen uint64, ctx context.Context, store cache.Store, key cache.Key) error {
	kf.mu.Lock()
	defer kf.mu.Unlock()
	if kf.gen != gen {
		return ErrStaleFill
	}
	return store.Delete(ctx, key)
}

// invalidate bumps the generation and deletes the entry in one critical
// section on the record, totally ordered against any concurrent fill write on
// the same key. It takes and releases its own reference around the section so
// the record lives even when no fill is in flight.
func (f *CacheFence) invalidate(ctx context.Context, store cache.Store, key cache.Key) error {
	kf := f.acquire(key)
	defer f.release(kf)
	kf.mu.Lock()
	defer kf.mu.Unlock()
	kf.gen++
	return store.Delete(ctx, key)
}

// Invalidator invalidates entries of cached loader bindings constructed from
// it. Create exactly one per application store (NewInvalidator), build every
// CacheOptions over that store from it, and call Invalidate after committing a
// mutation. The zero value is unusable; there is no global instance.
type Invalidator struct {
	fence   *CacheFence
	store   cache.Store
	onEvent func(CacheEvent)
}

// NewInvalidator creates the invalidation coordinator for one application
// store. The optional event callback receives CacheInvalidated and
// CacheInvalidationFailed outcomes (never invoked while holding internal
// locks; it must be concurrency-safe and must not log keys or scopes).
func NewInvalidator(store cache.Store, onEvent ...func(CacheEvent)) (*Invalidator, error) {
	if nilStore(store) {
		return nil, fmt.Errorf("%w: store required", ErrInvalidCacheConfig)
	}
	var events func(CacheEvent)
	if len(onEvent) > 0 {
		events = onEvent[0]
	}
	return &Invalidator{fence: newCacheFence(), store: store, onEvent: events}, nil
}

// Store returns the store the invalidator coordinates. BindCached receives it
// automatically through CacheOptions.Invalidator; applications normally keep
// their own reference from construction instead.
func (in *Invalidator) Store() cache.Store { return in.store }

// Invalidate removes the entry cached under the exact identity derived from
// route and scope. These must match the binding's LoaderCacheKey inputs: same
// route ID, same decoded path params, same query (missing distinct from empty,
// repeated values ordered), same application scope string. Tenant scope is
// part of the key, so one tenant's invalidation can never target another's
// entry.
//
// Linearization: the generation advance inside the record's critical section.
// After Invalidate returns nil, a later request misses and reloads fresh
// data, and an in-flight fill that began earlier can no longer repopulate the
// stale value (its write is suppressed). A fill may still return its own
// already-computed snapshot to its own request; only the cache write is
// blocked. Cancellation does not retroactively undo a completed invalidation.
//
// Failure: if the underlying store delete fails, the error wraps it unchanged
// (classify with errors.Is: cache.ErrClosed, context.Canceled, ...). Note the
// failure semantics: the generation has ALREADY advanced, so in-flight stale
// fills are suppressed even on delete failure, but the previously stored
// entry may remain in the store until its TTL expires — retry Invalidate; it
// is idempotent and safe.
//
// Repeated invalidation of a present, missing, or expired key is always a
// successful no-op in observable effect. Invalidation never retries and never
// deletes more than the exact key.
func (in *Invalidator) Invalidate(ctx context.Context, route routecontext.Route, scope string) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := LoaderCacheKey(route, scope)
	if err != nil {
		return fmt.Errorf("loader: invalidation key: %w", err)
	}
	return in.InvalidateKey(ctx, key)
}

// InvalidateKey removes the entry under an exact precomputed key (e.g. from
// LoaderCacheKey) with fencing identical to Invalidate.
func (in *Invalidator) InvalidateKey(ctx context.Context, key cache.Key) error {
	if ctx == nil {
		return ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return fmt.Errorf("loader: invalidation key: %w", err)
	}
	err := in.fence.invalidate(ctx, in.store, key)
	if in.onEvent != nil {
		if err != nil {
			in.onEvent(CacheInvalidationFailed)
		} else {
			in.onEvent(CacheInvalidated)
		}
	}
	return err
}
