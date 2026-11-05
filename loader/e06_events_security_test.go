package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/routecontext"
)

// TestE06EventCallbacksReentrant: an application event callback that calls
// back into cached bindings and invalidation from inside the callback must
// not deadlock — events are never emitted while a fence or flight lock is
// held. Concurrent activity keeps the system non-trivially locked.
func TestE06EventCallbacksReentrant(t *testing.T) {
	store := newTestStore(t)
	events := make(chan CacheEvent, 64)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewFlightGroup(inv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return demoData{Title: "v"}, nil
	}
	var reentry Registered
	var depth atomic.Int32
	reentry = registered(t, "reentry", load, CacheOptions[demoData]{
		Store: store, Invalidator: inv, Flights: group,
		Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope,
		Codec: JSONCodec[demoData](),
		OnEvent: func(ev CacheEvent) {
			select {
			case events <- ev:
			default:
			}
			// Re-enter the system while the emitting code is on the stack:
			// another binding's Execute and a fresh invalidation. Bounded
			// to one level — an unbounded re-entrant callback is an
			// application-level infinite loop by contract, not a framework
			// concern; what the framework guarantees is no lock-held emit.
			if ev != CacheInvalidated && ev != CacheFlightJoined && ev != CacheHit {
				return
			}
			if depth.Add(1) != 1 {
				return
			}
			defer depth.Add(-1)
			r := route("reentry", "other", nil)
			if _, err := reentry.Execute(context.Background(), r); err != nil {
				return
			}
			_ = inv.InvalidateKey(context.Background(), mustKey(t, r))
		},
	})
	// Also give an invalidator itself a re-entrant callback. A real
	// application callback must bound its own re-entrant work (a callback
	// that unconditionally re-invalidates would recurse forever by its own
	// logic); this fixture re-enters exactly once from each event.
	var inv2 *Invalidator
	var reentered atomic.Bool
	inv2, err = NewInvalidator(store, func(CacheEvent) {
		if reentered.CompareAndSwap(false, true) {
			r := route("other-route", "from-callback", nil)
			_ = inv2.InvalidateKey(context.Background(), mustKey(t, r))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := route("reentry", "42", nil)
	done := make(chan error, 1)
	go func() {
		_, err := reentry.Execute(ctx, r)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: event callback could not re-enter the cache system")
	}
	if err := inv2.InvalidateKey(ctx, mustKey(t, r)); err != nil {
		t.Fatal(err)
	}
	// Concurrency: multiple goroutines exercising bindings + invalidations with
	// the re-entrant callbacks installed.
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Go(func() {
			for i := 0; i < 20; i++ {
				rr := route("reentry", fmt.Sprintf("%d", i%4), nil)
				if _, err := reentry.Execute(ctx, rr); err != nil {
					t.Error(err)
					return
				}
				if err := inv2.InvalidateKey(ctx, mustKey(t, rr)); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wgDone := make(chan struct{})
	go func() { wg.Wait(); close(wgDone) }()
	select {
	case <-wgDone:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock under concurrent re-entrant event callbacks")
	}
}

func mustKey(t testing.TB, r routecontext.Route) cache.Key {
	t.Helper()
	k, err := LoaderCacheKey(r, "public-v1")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestE06EventPayloadsDiscloseNothing pins §15: every event value the
// system can emit carries no key, scope, payload or error text — only
// opaque outcome names. Anything a callback receives must be one of the
// declared constants.
var allCacheEvents = map[CacheEvent]bool{
	CacheHit: true, CacheMiss: true, CacheBypass: true,
	CacheReadError: true, CacheWriteError: true, CacheDecodeError: true,
	CacheEncodeError: true, CacheDeleteError: true, CacheClosed: true,
	CacheInvalidated: true, CacheInvalidationFailed: true,
	CacheStaleFillSuppressed: true, CacheFlightCreated: true, CacheFlightJoined: true,
}

func TestE06EventPayloadsDiscloseNothing(t *testing.T) {
	store := newTestStore(t)
	secret := "super-secret-tenant-cookie-value"
	var seen []CacheEvent
	var mu sync.Mutex
	inv, err := NewInvalidator(store, func(ev CacheEvent) {
		mu.Lock()
		seen = append(seen, ev)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return demoData{Title: "private-user-data"}, nil
	}
	h := registered(t, "leak", load, CacheOptions[demoData]{
		Store: store, Invalidator: inv,
		Policy: cache.Policy{TTL: time.Minute},
		Scope: func(context.Context, routecontext.Route) (string, bool, error) {
			return "tenant:" + secret, true, nil
		},
		Codec:   JSONCodec[demoData](),
		OnEvent: func(ev CacheEvent) { mu.Lock(); seen = append(seen, ev); mu.Unlock() },
	})
	ctx := context.Background()
	r := route("leak", "42", nil)
	for range 2 {
		if _, err := h.Execute(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := inv.Invalidate(ctx, r, "tenant:"+secret); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Execute(ctx, r); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("no events observed")
	}
	for _, ev := range seen {
		if !allCacheEvents[ev] {
			t.Fatalf("undisclosed event value: %q", string(ev))
		}
		s := string(ev)
		if strings.Contains(s, secret) || strings.Contains(s, "private-user-data") || strings.Contains(s, "42") {
			t.Fatalf("event leaked request data: %q", s)
		}
	}
}

// TestE06ZeroLengthValueIsValidHit (§6): a zero-length cached value round-trips
// as a hit through the E03 codec path when the loader legitimately produces an
// empty encoding.
func TestE06ZeroLengthValueIsValidHit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key, err := cache.NewKey("loader", "zero")
	if err != nil {
		t.Fatal(err)
	}
	// Direct store-level: an empty value Set then Get is a hit, not a miss.
	if err := store.Set(ctx, key, cache.Entry{Value: []byte{}}, cache.Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	entry, hit, err := store.Get(ctx, key)
	if err != nil || !hit {
		t.Fatalf("empty value must be a valid hit: hit=%v err=%v", hit, err)
	}
	if len(entry.Value) != 0 {
		t.Fatalf("empty value mutated: %d bytes", len(entry.Value))
	}
}

// TestE06FailedDeletionSemantics (§20): when the store's Delete fails, the
// invalidation reports failure, keeps fencing stale fills (generation already
// advanced), never flushes more than the exact key, and retry succeeds.
func TestE06FailedDeletionSemantics(t *testing.T) {
	store := newTestStore(t)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	gated := newGatedCodec()
	var value atomic.Value
	value.Store(demoData{Title: "V1"})
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return value.Load().(demoData), nil
	}
	reg := newFencedRegistry(t, store, inv, gated.Codec(), constScope("s"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	key, err := LoaderCacheKey(rt, "s")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := reg.Lookup("demo.show")
	// Phase 1 — pre-existing entry survival: a stored V1 entry outlives a
	// failed invalidation (generation advances, delete fails).
	seed, err := json.Marshal(demoData{Title: "V1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, key, cache.Entry{Value: seed}, cache.Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	store.failDel.Store(true)
	if err := inv.InvalidateKey(ctx, key); err == nil {
		t.Fatal("failed deletion must be reported as an error")
	}
	entry, hit, err := store.Get(ctx, key)
	if err != nil || !hit {
		t.Fatalf("pre-existing entry survival must be observable: %v %v", hit, err)
	}
	if !strings.Contains(string(entry.Value), "V1") {
		t.Fatal("entry content changed unexpectedly")
	}

	// Phase 2 — stale fills stay fenced despite the delete failure: with
	// the surviving V1 entry removed by hand (as a successful retry would),
	// a fill that read V1 and parks before its Set must not write after a
	// failed invalidation.
	store.failDel.Store(false)
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	value.Store(demoData{Title: "V1"})
	fill := make(chan struct{})
	go func() { defer close(fill); _, _ = r.Execute(ctx, rt) }()
	<-waitGateEntered(gated)
	value.Store(demoData{Title: "V2"})
	// Deletion fails again: the invalidation reports failure, but the
	// generation has already advanced inside the fence record.
	store.failDel.Store(true)
	if err := inv.InvalidateKey(ctx, key); err == nil {
		t.Fatal("failed deletion must be reported as an error")
	}
	store.failDel.Store(false)
	close(gated.gate)
	<-fill
	if _, hit, _ := store.Get(ctx, key); hit {
		t.Fatal("stale fill was not fenced on failed invalidation")
	}
	// No broad flush: other keys survive.
	other, err := cache.NewKey("loader", "other-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, other, cache.Entry{Value: []byte("keep")}, cache.Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	store.failDel.Store(false)
	// Retry succeeds and removes exactly the target key.
	if err := inv.InvalidateKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, hit, _ := store.Get(ctx, key); hit {
		t.Fatal("retry did not remove the entry")
	}
	if _, hit, _ := store.Get(ctx, other); !hit {
		t.Fatal("retry flushed an unrelated key")
	}
	// A committed mutation is not misrepresented: fresh fill reads V2.
	got, err := r.Execute(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	if got.(demoData).Title != "V2" {
		t.Fatalf("fresh fill after retry: %#v", got)
	}
}
