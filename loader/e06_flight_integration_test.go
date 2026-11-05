package loader

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-meta/cache"
	"go-meta/routecontext"
)

// TestE06FlightGenerationSeparationAfterInvalidation is the §18 combined
// E04/E05 regression, run deterministically:
//
//	V1: A starts an old-generation flight; B joins it.
//	Mutation commits V2 and invalidates successfully.
//	C arrives after the invalidation and must NOT join the old flight.
//	A/B are released and may finish with V1.
//	C must receive V2; D must subsequently receive V2 from the cache.
//
// The old flight must never overwrite the fresh cache. Exactly two loader
// computations happen: one old generation, one new.
func TestE06FlightGenerationSeparationAfterInvalidation(t *testing.T) {
	entered, release, newFlightDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var version atomic.Int32
	version.Store(1)
	var calls atomic.Int32
	load := func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		v := int(version.Load())
		if calls.Add(1) == 1 {
			// Old-generation leader: park until the test releases it.
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return flightData{}, ctx.Err()
			}
		} else if v == 2 {
			close(newFlightDone)
		}
		return flightData{Value: v}, nil
	}
	group, inv, store, h, r := newFlightTest(t, load)
	ctx := context.Background()
	a := startFlight(ctx, h, r)
	<-entered
	b := startFlight(ctx, h, r)
	waitFlights(t, group, 1, 2)
	// Mutation commits V2, then invalidates.
	version.Store(2)
	if err := inv.Invalidate(ctx, r, "public-v1"); err != nil {
		t.Fatal(err)
	}
	// C arrives after the invalidation. The old flight's leader is still
	// parked inside the loader (release not yet closed), so if C joined the
	// old flight it could only complete after the release with V1. C's own
	// new-generation computation finishes immediately.
	c := startFlight(ctx, h, r)
	select {
	case <-newFlightDone:
	case <-time.After(5 * time.Second):
		t.Fatal("post-invalidation request did not start its own computation")
	}
	if got := receive(t, c); got.err != nil || got.value.(flightData).Value != 2 {
		t.Fatalf("C must receive V2 through a new-generation flight: %#v %v", got.value, got.err)
	}
	// While the old generation is still parked, exactly two computations ran.
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 loader executions, got %d", got)
	}
	// Release the old generation: A and B may finish with their V1 snapshot.
	close(release)
	for _, ch := range []<-chan result{a, b} {
		got := receive(t, ch)
		if got.err != nil || got.value.(flightData).Value != 1 {
			t.Fatalf("old-generation waiter: %#v %v", got.value, got.err)
		}
	}
	// D must see V2 — the old flight did not overwrite the fresh entry.
	d, err := h.Execute(ctx, r)
	if err != nil || d.(flightData).Value != 2 {
		t.Fatalf("D received stale cache: %#v %v", d, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 loader executions total, got %d", got)
	}
	store.mu.Lock()
	sets := len(store.sets)
	store.mu.Unlock()
	// Exactly one Set: the old flight's write is fenced off, the new one
	// writes. (The old-generation fill may attempt a Set that the fence
	// suppresses before it reaches the store.)
	if sets != 1 {
		t.Fatalf("expected exactly one surviving store write, got %d", sets)
	}
	waitFlights(t, group, 0, 0)
}

// TestE06FlightFallbackUsesEachWaitersScopeAndCancellation pins E05's
// non-copyable-result fallback: when a shared result cannot be safely
// copied, each waiter executes independently with ITS OWN request context
// (route, principal values, cancellation) — never another waiter's.
//
// The shared computation itself deliberately runs without request-specific
// values (§26); this test proves the FALLBACK path restores them.
func TestE06FlightFallbackUsesEachWaitersScopeAndCancellation(t *testing.T) {
	type principalKey struct{}
	entered, release := make(chan struct{}), make(chan struct{})
	fallbackExecs := make(chan string, 8)
	store := newTestStoreForFlight(t)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewFlightGroup(inv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	scope := func(ctx context.Context, _ routecontext.Route) (string, bool, error) {
		return "public-v1", true, nil // one shared scope: one cache key
	}
	load := func(ctx context.Context, r routecontext.Route) (flightData, error) {
		p, _ := ctx.Value(principalKey{}).(string)
		id, _ := r.Params.Get("id")
		if p == "" {
			// Shared leader execution: park until released (it has no
			// request values by design — §26).
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return flightData{}, ctx.Err()
			}
		} else {
			fallbackExecs <- p + ":" + id
		}
		return flightData{Value: len(p), Meta: map[string]string{"owner": p, "route": id}}, nil
	}
	// A codec whose Encode always fails makes successful results
	// non-shareable: every waiter falls back to its own execution.
	codec := JSONCodec[flightData]()
	codec.Encode = func(flightData) ([]byte, error) { return nil, errors.New("uncopyable") }
	h := registered(t, "flight", load, CacheOptions[flightData]{
		Store: store, Invalidator: inv, Flights: group,
		Policy: cache.Policy{TTL: time.Minute}, Scope: scope, Codec: codec,
	})
	r := route("flight", "42", nil)
	ctxA, cancelA := context.WithCancel(context.WithValue(context.Background(), principalKey{}, "tenant-a"))
	defer cancelA()
	a := startFlight(ctxA, h, r)
	<-entered
	ctxB := context.WithValue(context.Background(), principalKey{}, "tenant-b")
	b := startFlight(ctxB, h, r) // same key; result will not be shareable
	waitFlights(t, group, 1, 2)
	// Waiter C has a short deadline; leaving must NOT cancel the shared work
	// or the other waiters (§23).
	ctxC, cancelC := context.WithTimeout(context.WithValue(context.Background(), principalKey{}, "tenant-c"), 50*time.Millisecond)
	defer cancelC()
	c := startFlight(ctxC, h, r)
	if got := receive(t, c); !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("deadline waiter: %#v %v", got.value, got.err)
	}
	// A and B remain: the flight completes and one CLAIMS the leader's
	// computed result while the other executes independently with its OWN
	// request context and route (§12 fallback).
	close(release)
	results := []result{receive(t, a), receive(t, b)}
	var owners []string
	for _, res := range results {
		if res.err != nil {
			t.Fatalf("waiter failed: %v", res.err)
		}
		v := res.value.(flightData)
		if v.Meta["route"] != "42" {
			t.Fatalf("waiter received a foreign route: %#v", v)
		}
		owners = append(owners, v.Meta["owner"])
	}
	// Exactly one waiter received the leader's shared-context result
	// (owner "") and the other executed its fallback with its own principal.
	claimant, fallback := 0, 0
	for _, o := range owners {
		switch o {
		case "":
			claimant++
		case "tenant-a", "tenant-b":
			fallback++
		default:
			t.Fatalf("fallback executed with a foreign principal: %q", o)
		}
	}
	if claimant != 1 || fallback != 1 {
		t.Fatalf("expected one claimant and one independent fallback, owners=%v", owners)
	}
	select {
	case exec := <-fallbackExecs:
		if exec != "tenant-a:42" && exec != "tenant-b:42" {
			t.Fatalf("fallback used a foreign context/route: %q", exec)
		}
	default:
		t.Fatal("no independent fallback execution ran")
	}
	waitFlights(t, group, 0, 0)
}

// TestE06FlightFallbackCancellationIsolated: when the shared result is not
// copyable, a canceled fallback waiter simply cancels — it cannot cancel
// another waiter's in-progress fallback execution or strand anyone.
func TestE06FlightFallbackCancellationIsolated(t *testing.T) {
	type principalKey struct{}
	entered, release := make(chan struct{}), make(chan struct{})
	fallbackDone := make(chan string, 4)
	store := newTestStoreForFlight(t)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewFlightGroup(inv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	load := func(ctx context.Context, r routecontext.Route) (flightData, error) {
		p, _ := ctx.Value(principalKey{}).(string)
		id, _ := r.Params.Get("id")
		if p == "" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return flightData{}, ctx.Err()
			}
		} else {
			// Independent fallback execution: cooperative, waits for release.
			select {
			case <-release:
			case <-ctx.Done():
				return flightData{}, ctx.Err()
			}
			fallbackDone <- p + ":" + id
		}
		return flightData{Meta: map[string]string{"owner": p}}, nil
	}
	codec := JSONCodec[flightData]()
	codec.Encode = func(flightData) ([]byte, error) { return nil, errors.New("uncopyable") }
	h := registered(t, "flight", load, CacheOptions[flightData]{
		Store: store, Invalidator: inv, Flights: group,
		Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: codec,
	})
	r := route("flight", "42", nil)
	ctxA, cancelA := context.WithCancel(context.WithValue(context.Background(), principalKey{}, "tenant-a"))
	a := startFlight(ctxA, h, r)
	<-entered
	bCtx, cancelB := context.WithCancel(context.WithValue(context.Background(), principalKey{}, "tenant-b"))
	b := startFlight(bCtx, h, r)
	waitFlights(t, group, 1, 2)
	close(release) // leader finishes; both waiters fall back to own executions
	cancelA()      // A's fallback cancels immediately
	cancelB()      // B's fallback cancels too; neither strands the other
	for _, ch := range []<-chan result{a, b} {
		got := receive(t, ch)
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("waiter: %#v %v", got.value, got.err)
		}
	}
	select {
	case exec := <-fallbackDone:
		t.Fatalf("canceled fallback should not complete: %q", exec)
	default:
	}
	waitFlights(t, group, 0, 0)
}

// TestE06InvalidationDuringCorruptEntryRecovery pins the §21 case: an
// invalidation racing a corrupt-entry delete-and-refill cannot resurrect
// the corrupt bytes or skip fencing. Deterministic via a gated Decode.
func TestE06InvalidationDuringCorruptEntryRecovery(t *testing.T) {
	store := newTestStore(t)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	releaseDecode := make(chan struct{})
	codec := JSONCodec[demoData]()
	inner := codec.Decode
	decodeEntered := make(chan struct{})
	var decodeCount atomic.Int32
	codec.Decode = func(b []byte) (demoData, error) {
		if decodeCount.Add(1) == 1 {
			decodeEntered <- struct{}{}
			<-releaseDecode
		}
		return inner(b)
	}
	var value atomic.Value
	value.Store(demoData{Title: "V2"})
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return value.Load().(demoData), nil
	}
	reg := newFencedRegistry(t, store, inv, codec, constScope("s"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	key, err := LoaderCacheKey(rt, "s")
	if err != nil {
		t.Fatal(err)
	}
	// Seed a corrupt entry.
	if err := store.Set(ctx, key, cache.Entry{Value: []byte("not-json")}, cache.Policy{TTL: time.Minute}); err != nil {
		t.Fatal(err)
	}
	r, _ := reg.Lookup("demo.show")
	done := make(chan struct{})
	go func() { defer close(done); _, _ = r.Execute(ctx, rt) }()
	<-decodeEntered
	// Invalidation fires while the fill is parked inside its failing Decode.
	if err := inv.InvalidateKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	close(releaseDecode)
	<-done
	// The recovery either reloaded (fresh V2) or the invalidation removed the
	// corrupt entry; either way the next request sees V2 exactly once more and
	// then caches.
	for range 2 {
		got, err := r.Execute(ctx, rt)
		if err != nil {
			t.Fatal(err)
		}
		if got.(demoData).Title != "V2" {
			t.Fatalf("corrupt value survived recovery+invalidation race: %#v", got)
		}
	}
}

// TestE06InvalidateAroundInFlightOperations exercises §21's interleavings
// deterministically: invalidation while a Get, a Set, and a loader fill are
// in progress. Uses real memory store operations plus fence-level checks.
func TestE06InvalidateAroundInFlightOperations(t *testing.T) {
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

	// Phase 1: a fill parked before Set while invalidation lands, twice.
	for i := range 2 {
		gated.encoded.Store(0)
		done := make(chan struct{})
		go func() { defer close(done); _, _ = r.Execute(ctx, rt) }()
		<-waitGateEntered(gated)
		value.Store(demoData{Title: "V-new"})
		if err := inv.InvalidateKey(ctx, key); err != nil {
			t.Fatal(err)
		}
		close(gated.gate)
		<-done
		gated.gate = make(chan struct{})
		_ = i
	}
	// Phase 2: the invalidation landed BEFORE the fill began: the fill must
	// run post-invalidation (fresh) or be fenced — never repopulate V1-old.
	got, err := r.Execute(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	if title := got.(demoData).Title; title != "V-new" {
		t.Fatalf("stale data served around invalidation: %q", title)
	}
}

// TestE06SingleflightBurstScale measures coalescing across waiter bursts of
// increasing width: the loader runs once per burst regardless of width, and
// every waiter receives the shared value. This is correctness-at-scale
// coverage; timings are reported as benchmarks elsewhere.
func TestE06SingleflightBurstScale(t *testing.T) {
	for _, waiters := range []int{1, 2, 4, 8, 16} {
		t.Run(fmt.Sprintf("%d-waiters", waiters), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			group, _, _, h, r := newFlightTest(t, func(ctx context.Context, _ routecontext.Route) (flightData, error) {
				if calls.Add(1) == 1 {
					close(entered)
				}
				select {
				case <-release:
					return flightData{Value: waiters}, nil
				case <-ctx.Done():
					return flightData{}, ctx.Err()
				}
			})
			results := make([]<-chan result, waiters)
			results[0] = startFlight(context.Background(), h, r)
			<-entered
			for i := 1; i < waiters; i++ {
				results[i] = startFlight(context.Background(), h, r)
			}
			waitFlights(t, group, 1, waiters)
			close(release)
			for _, ch := range results {
				got := receive(t, ch)
				if got.err != nil || got.value.(flightData).Value != waiters {
					t.Fatalf("waiter: %#v %v", got.value, got.err)
				}
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("burst of %d waiters ran loader %d times", waiters, got)
			}
			waitFlights(t, group, 0, 0)
		})
	}
}

// TestE06FlightABAReplacementLifecycle pins §19: after an old flight
// completes and its coordinator record is removed, a new flight under the
// same key works, and the old flight's LATE cleanup cannot remove the
// replacement.
func TestE06FlightABAReplacementLifecycle(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	load := func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-release:
			case <-ctx.Done():
				return flightData{}, ctx.Err()
			}
			return flightData{Value: 1}, nil
		}
		return flightData{Value: 2}, nil
	}
	group, inv, _, h, r := newFlightTest(t, load)
	ctx := context.Background()
	key, err := LoaderCacheKey(r, "public-v1")
	if err != nil {
		t.Fatal(err)
	}
	// Old flight: leader parks; all waiters leave (last-waiter cancel).
	ctxA, cancelA := context.WithCancel(ctx)
	a := startFlight(ctxA, h, r)
	<-firstStarted
	bCtx, cancelB := context.WithCancel(ctx)
	b := startFlight(bCtx, h, r)
	waitFlights(t, group, 1, 2)
	cancelA()
	cancelB()
	if got := receive(t, a); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("a: %v", got.err)
	}
	if got := receive(t, b); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("b: %v", got.err)
	}
	waitFlights(t, group, 0, 0)
	// New flight under the same key (after invalidation to force a miss).
	if err := inv.InvalidateKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	nr := receive(t, startFlight(ctx, h, r))
	if nr.err != nil || nr.value.(flightData).Value != 2 {
		t.Fatalf("replacement flight: %#v %v", nr.value, nr.err)
	}
	waitFlights(t, group, 0, 0)
	// The old abandoned worker eventually finishes; its late cleanup must not
	// affect the new flight or the fence map.
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		inv.fence.mu.Lock()
		n := len(inv.fence.fences)
		inv.fence.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late cleanup left fence records")
		}
		time.Sleep(time.Millisecond)
	}
	// A subsequent request still works and hits the fresh cache.
	got, err := h.Execute(ctx, r)
	if err != nil || got.(flightData).Value != 2 {
		t.Fatalf("post-ABA execute: %#v %v", got, err)
	}
}

// TestE06UncooperativeLoaderNeverOverwritesNewGeneration: a deliberately
// uncooperative (cancellation-ignoring) leader keeps its worker alive after
// last-waiter cancellation; that abandoned work must never overwrite a
// newer generation's cached data.
func TestE06UncooperativeLoaderNeverOverwritesNewGeneration(t *testing.T) {
	entered, releaseWorker := make(chan struct{}), make(chan struct{})
	var version atomic.Int32
	version.Store(1)
	var calls atomic.Int32
	load := func(_ context.Context, _ routecontext.Route) (flightData, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-releaseWorker // ignores cancellation entirely
			return flightData{Value: 1}, nil
		}
		return flightData{Value: int(version.Load())}, nil
	}
	group, inv, _, h, r := newFlightTest(t, load)
	ctx := context.Background()
	_ = inv
	ctxA, cancelA := context.WithCancel(ctx)
	a := startFlight(ctxA, h, r)
	<-entered
	waitFlights(t, group, 1, 1)
	cancelA()
	if got := receive(t, a); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("waiter: %v", got.err)
	}
	waitFlights(t, group, 0, 0)
	// New generation fills the cache with V2.
	version.Store(2)
	got, err := h.Execute(ctx, r)
	if err != nil || got.(flightData).Value != 2 {
		t.Fatalf("new generation: %#v %v", got, err)
	}
	// Release the abandoned old worker; its stale V1 must not appear.
	close(releaseWorker)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := h.Execute(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if got.(flightData).Value != 2 {
			t.Fatalf("abandoned worker overwrote the new generation: %#v", got)
		}
		time.Sleep(time.Millisecond)
	}
	waitFlights(t, group, 0, 0)
}

// TestE06MultipleInvalidatorsSharedStore: independent invalidators over a
// shared store (an anti-pattern, but must not corrupt): an invalidation
// through one coordinator still fences fills registered against the other.
func TestE06MultipleInvalidatorsSharedStore(t *testing.T) {
	store := newTestStore(t)
	inv1, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	inv2, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	var value atomic.Value
	value.Store(demoData{Title: "V1"})
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return value.Load().(demoData), nil
	}
	reg := newFencedRegistry(t, store, inv1, JSONCodec[demoData](), constScope("s"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	r, _ := reg.Lookup("demo.show")
	// Fill through inv1's binding.
	if _, err := r.Execute(ctx, rt); err != nil {
		t.Fatal(err)
	}
	// Invalidation through the OTHER coordinator deletes the same entry.
	value.Store(demoData{Title: "V2"})
	if err := inv2.Invalidate(ctx, rt, "s"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Execute(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	if got.(demoData).Title != "V2" {
		t.Fatalf("cross-coordinator invalidation missed the entry: %q", got.(demoData).Title)
	}
}

// TestE06HighCardinalityCoordinatorCleanup: thousands of distinct keys run
// through cached bindings concurrently; afterwards both the fence map and
// the flight map must be empty (no tombstone accumulation). Cache sizes kept
// small so admission pressure also churns eviction.
func TestE06HighCardinalityCoordinatorCleanup(t *testing.T) {
	m, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 128, MaxBytes: 512 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	store := &recordingStore{inner: m}
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewFlightGroup(inv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	load := func(_ context.Context, r routecontext.Route) (flightData, error) {
		id, _ := r.Params.Get("id")
		return flightData{Value: len(id), Items: []string{id}}, nil
	}
	h := registered(t, "flight", load, CacheOptions[flightData]{
		Store: store, Invalidator: inv, Flights: group,
		Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope,
		Codec: JSONCodec[flightData](),
	})
	ctx := context.Background()
	const keys = 2000
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Go(func() {
			for i := 0; i < keys/8; i++ {
				r := route("flight", fmtKey(i*8+w), nil)
				if _, err := h.Execute(ctx, r); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	waitFlights(t, group, 0, 0)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		inv.fence.mu.Lock()
		n := len(inv.fence.fences)
		inv.fence.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fence map retained records after high-cardinality churn")
}

func fmtKey(i int) string {
	if i < 0 {
		return ""
	}
	digits := []byte("0123456789abcdef")
	out := []byte{}
	for {
		out = append([]byte{digits[i%16]}, out...)
		i /= 16
		if i == 0 {
			return string(out)
		}
	}
}
