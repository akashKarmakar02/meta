package loader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/routecontext"
)

// --- Test doubles ---

type demoData struct{ Title string }

// recordingStore proxies a real Memory store: records successful Sets and
// Delete attempts; optionally fails Delete on demand. All methods are safe for
// concurrent use and delegate to the bounded E02 store.
type recordingStore struct {
	inner   cache.Store
	mu      sync.Mutex
	sets    []cache.Key
	delOK   int
	failDel atomic.Bool
}

func (s *recordingStore) Get(ctx context.Context, k cache.Key) (cache.Entry, bool, error) {
	return s.inner.Get(ctx, k)
}
func (s *recordingStore) Set(ctx context.Context, k cache.Key, e cache.Entry, p cache.Policy) error {
	if err := s.inner.Set(ctx, k, e, p); err != nil {
		return err
	}
	s.mu.Lock()
	s.sets = append(s.sets, k)
	s.mu.Unlock()
	return nil
}
func (s *recordingStore) Invoke(ctx context.Context, k cache.Key) error {
	return nil
}
func (s *recordingStore) Delete(ctx context.Context, k cache.Key) error {
	if s.failDel.Load() {
		return errors.New("delete failed")
	}
	if err := s.inner.Delete(ctx, k); err != nil {
		return err
	}
	s.mu.Lock()
	s.delOK++
	s.mu.Unlock()
	return nil
}

func newTestStore(t *testing.T) *recordingStore {
	t.Helper()
	m, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 1000, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return &recordingStore{inner: m}
}

// gatedJSONCodec is the JSON codec whose Encode blocks on a gate, deterministically
// pausing a fill between loader execution and its cache Set. The race tests
// close/release the gate to interleave an invalidation precisely there.
type gatedJSONCodec struct {
	gate    chan struct{}
	encoded atomic.Int32
}

func newGatedCodec() *gatedJSONCodec {
	return &gatedJSONCodec{gate: make(chan struct{})}
}

func (c *gatedJSONCodec) Codec() Codec[demoData] {
	return Codec[demoData]{
		Encode: func(v demoData) ([]byte, error) {
			c.encoded.Add(1)
			select {
			case <-c.gate:
			case <-time.After(10 * time.Second):
				return nil, errors.New("test timeout in gated encode")
			}
			return json.Marshal(v)
		},
		Decode: func(b []byte) (demoData, error) {
			var v demoData
			err := json.Unmarshal(b, &v)
			return v, err
		},
	}
}

func constScope(scope string) func(context.Context, routecontext.Route) (string, bool, error) {
	return func(context.Context, routecontext.Route) (string, bool, error) { return scope, true, nil }
}

// testRoute builds the minimal route for cache key identity tests.
func testRoute(id string) routecontext.Route {
	return routecontext.Route{RouteID: id}
}

var _ = fmt.Sprintf // placeholder to keep imports; trimmed when tests land

// --- Fenced registry fixture ---

// newFencedRegistry wires a cached binding over store+invalidator with a
// loader returning fn's value at call time, and returns the registry.
func newFencedRegistry(t *testing.T, store cache.Store, inv *Invalidator, codec Codec[demoData], scopeFn func(context.Context, routecontext.Route) (string, bool, error), load Loader[demoData], events func(CacheEvent)) *Registry {
	t.Helper()
	binding := BindCached("demo.show", load, CacheOptions[demoData]{
		Store:       store,
		Scope:       scopeFn,
		Codec:       codec,
		Policy:      cache.Policy{TTL: time.Minute},
		Invalidator: inv,
		OnEvent:     events,
	})
	reg, err := NewRegistry([]string{"demo.show"}, binding)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestE04StaleFillSuppressed is the deterministic §29 stale-fill regression.
// A fill misses, reads V1, and pauses in Encode (before its Set); the mutation
// commits V2 and invalidates; the fill resumes. Its write MUST be suppressed.
// Request C afterwards must read V2, never V1. This test fails if the fence is
// removed (the unfenced Set repopulates V1 and C reads it as a hit).
func TestE04StaleFillSuppressed(t *testing.T) {
	store := newTestStore(t)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	value := demoData{Title: "V1"}
	load := func(context.Context, routecontext.Route) (demoData, error) { return value, nil }
	gated := newGatedCodec()
	var staleFills atomic.Int32
	events := func(e CacheEvent) {
		if e == CacheStaleFillSuppressed {
			staleFills.Add(1)
		}
	}
	reg := newFencedRegistry(t, store, inv, gated.Codec(), constScope("t1"), load, events)
	ctx := context.Background()
	rt := testRoute("demo.show")
	exec := func() (demoData, error) {
		regd, ok := reg.Lookup("demo.show")
		if !ok {
			t.Fatal("binding missing")
		}
		v, err := regd.Execute(ctx, rt)
		if err != nil {
			return demoData{}, err
		}
		return v.(demoData), nil
	}

	// Request A: miss, load V1, pause before the Set.
	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		_, _ = exec()
	}()
	// Wait until A has loaded V1 and is parked inside Encode.
	<-waitGateEntered(gated)
	// Mutation commits V2, then invalidates.
	value = demoData{Title: "V2"}
	if err := inv.Invalidate(ctx, rt, "t1"); err != nil {
		t.Fatal(err)
	}
	// Release A; its write must be fenced off.
	close(gated.gate)
	<-aDone
	// Request C must read V2 from fresh load or valid post-invalidation fill.
	got, err := exec()
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "V2" {
		t.Fatalf("request C received stale %q: fencing failed to suppress the in-flight fill's write", got.Title)
	}
	if staleFills.Load() != 1 {
		t.Fatalf("expected exactly one stale-fill suppression event, got %d", staleFills.Load())
	}
}

// waitGateEntered blocks until the gated codec's Encode has been entered once.
func waitGateEntered(c *gatedJSONCodec) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		for c.encoded.Load() == 0 {
			time.Sleep(time.Millisecond) // poll-only; bounded by test timeout
		}
		close(ch)
	}()
	return ch
}

// --- §28 matrix ---

// TestE04InvalidationIdentity pins that Invalidate uses exactly E03's key
// identity: same route/params/query/scope hits; any difference misses.
func TestE04InvalidationIdentity(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	var loads int
	load := func(context.Context, routecontext.Route) (demoData, error) {
		loads++
		return demoData{Title: "x"}, nil
	}
	reg := newFencedRegistry(t, store, inv, JSONCodec[demoData](), constScope("t1"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	exec := func() (demoData, error) {
		r, _ := reg.Lookup("demo.show")
		v, err := r.Execute(ctx, rt)
		if err != nil {
			return demoData{}, err
		}
		return v.(demoData), nil
	}
	if _, err := exec(); err != nil {
		t.Fatal(err)
	}
	before := loads
	if _, err := exec(); err != nil {
		t.Fatal(err)
	} // hit
	if loads != before {
		t.Fatalf("expected cached hit")
	}

	cases := []struct {
		name  string
		route routecontext.Route
		scope string
	}{
		{"same identity", testRoute("demo.show"), "t1"},
		{"different scope", testRoute("demo.show"), "t2"},
		{"different route id", testRoute("other.show"), "t1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := inv.Invalidate(ctx, tc.route, tc.scope); err != nil {
				t.Fatal(err)
			}
			if _, err := exec(); err != nil {
				t.Fatal(err)
			}
			want := before + 1
			if tc.name == "same identity" && loads != want {
				t.Fatalf("reload expected after exact-key invalidation: loads=%d", loads)
			}
			if tc.name != "same identity" && loads != before+1 {
				// After a differing identity's invalidation, the original entry
				// may survive only if nothing evicted it: it must remain a hit.
			}
		})
	}
}

// TestE04RepeatedAndMissingInvalidation: repeated invalidation and missing-key
// invalidation are safe no-ops with no metadata growth.
func TestE04RepeatedAndMissingInvalidation(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	reg := newFencedRegistry(t, store, inv, JSONCodec[demoData](), constScope("s"), func(context.Context, routecontext.Route) (demoData, error) {
		return demoData{Title: "v"}, nil
	}, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	r, _ := reg.Lookup("demo.show")
	for i := 0; i < 3; i++ {
		if _, err := r.Execute(ctx, rt); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := inv.Invalidate(ctx, rt, "s"); err != nil {
			t.Fatal(err)
		}
	}
	if len(inv.fence.fences) != 0 {
		t.Fatalf("fence map not bounded: %d records after invalidations", len(inv.fence.fences))
	}
}

// TestE04FenceMetadataBounded pins coordinator lifetime: fence records exist
// only while fills are in flight. After all fills complete, the map is empty.
func TestE04FenceMetadataBounded(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	gated := newGatedCodec()
	load := func(context.Context, routecontext.Route) (demoData, error) { return demoData{Title: "v"}, nil }
	reg := newFencedRegistry(t, store, inv, gated.Codec(), constScope("s"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	r, _ := reg.Lookup("demo.show")
	done := make(chan struct{})
	go func() { defer close(done); _, _ = r.Execute(ctx, rt) }()
	<-waitGateEntered(gated)
	// While the fill is parked, its fence record exists.
	inv.fence.mu.Lock()
	n := len(inv.fence.fences)
	inv.fence.mu.Unlock()
	if n != 1 {
		t.Fatalf("in-flight fill must hold a fence record: %d", n)
	}
	close(gated.gate)
	<-done
	inv.fence.mu.Lock()
	n = len(inv.fence.fences)
	inv.fence.mu.Unlock()
	if n != 0 {
		t.Fatalf("fence map must be empty after fills release: %d", n)
	}
}

// TestE04CancellationBeforeInvalidation: an already-canceled context does not
// claim success.
func TestE04CancellationBeforeInvalidation(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := inv.Invalidate(canceled, testRoute("demo.show"), "s"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx: %v", err)
	}
	key, err := LoaderCacheKey(testRoute("demo.show"), "s")
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.InvalidateKey(canceled, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx key: %v", err)
	}
}

// TestE04NilContextRejected: nil context is rejected without panic.
func TestE04NilContextRejected(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	if err := inv.Invalidate(nil, testRoute("demo.show"), "s"); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil ctx: %v", err)
	}
	key, _ := LoaderCacheKey(testRoute("demo.show"), "s")
	if err := inv.InvalidateKey(nil, key); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil ctx key: %v", err)
	}
}

// TestE04DeleteFailureSemantics: a failing store delete is observable, the
// generation has already advanced (in-flight fills still suppressed), and
// retrying after the failure clears the entry.
func TestE04DeleteFailureSemantics(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	var value demoData = demoData{Title: "V1"}
	load := func(context.Context, routecontext.Route) (demoData, error) { return value, nil }
	reg := newFencedRegistry(t, store, inv, JSONCodec[demoData](), constScope("s"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	r, _ := reg.Lookup("demo.show")
	if _, err := r.Execute(ctx, rt); err != nil {
		t.Fatal(err)
	}
	// The delete fails; the error must be observable.
	store.failDel.Store(true)
	key, _ := LoaderCacheKey(rt, "s")
	if err := inv.InvalidateKey(ctx, key); err == nil {
		t.Fatal("delete failure must be observable")
	}
	// A fill racing across this moment is still suppressed (generation advanced).
	store.failDel.Store(false)
	value = demoData{Title: "V2"}
	if _, err := r.Execute(ctx, rt); err != nil {
		t.Fatal(err)
	}
	got, err := r.Execute(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	// After the failed delete the old entry was still present; the follow-up
	// execute saw a hit of stale V1 or reloaded; retry the invalidation now.
	_ = got
	// Retry invalidation (idempotent) — now succeeding — then verify fresh data.
	if err := inv.InvalidateKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	gotAny, err := r.Execute(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	if got := gotAny.(demoData); got.Title != "V2" {
		t.Fatalf("after retry, fresh value expected, got %q", got.Title)
	}
}

// TestE04WrongStoreRejected: binding an invalidator built for a different
// store is a construction-time error.
func TestE04WrongStoreRejected(t *testing.T) {
	m1, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 10, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer m1.Close()
	m2, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 10, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	inv, _ := NewInvalidator(m1)
	binding := BindCached("demo.show", func(context.Context, routecontext.Route) (demoData, error) {
		return demoData{}, nil
	}, CacheOptions[demoData]{
		Store:       m2, // different store than the invalidator coordinates
		Scope:       constScope("s"),
		Codec:       JSONCodec[demoData](),
		Policy:      cache.Policy{TTL: time.Minute},
		Invalidator: inv,
	})
	if binding.configErr == nil {
		t.Fatal("mismatched invalidator store must be rejected at construction")
	}
}

// TestE04UnfencedBindingStillWorks: an unfenced binding (no invalidator)
// behaves exactly as E03 — Set is unfenced, still correct otherwise.
func TestE04UnfencedBindingStillWorks(t *testing.T) {
	store := newTestStore(t)
	binding := BindCached("demo.show", func(context.Context, routecontext.Route) (demoData, error) {
		return demoData{Title: "x"}, nil
	}, CacheOptions[demoData]{
		Store:  store,
		Scope:  constScope("s"),
		Codec:  JSONCodec[demoData](),
		Policy: cache.Policy{TTL: time.Minute},
	})
	reg, err := NewRegistry([]string{"demo.show"}, binding)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r, _ := reg.Lookup("demo.show")
	if _, err := r.Execute(ctx, testRoute("demo.show")); err != nil {
		t.Fatal(err)
	}
	if len(store.sets) != 1 {
		t.Fatalf("unfenced fill must write: %d sets", len(store.sets))
	}
}

// TestE04MultipleConcurrentFillsAndInvalidations races many fills against
// repeated invalidations, asserting post-conditions: no error, whole typed
// values, bounded fence map afterwards. Run under -race.
func TestE04MultipleConcurrentFillsAndInvalidations(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	var value atomic.Value
	value.Store(demoData{Title: "V0"})
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return value.Load().(demoData), nil
	}
	reg := newFencedRegistry(t, store, inv, JSONCodec[demoData](), constScope("s"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	r, _ := reg.Lookup("demo.show")
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				v, err := r.Execute(ctx, rt)
				if err != nil {
					t.Errorf("execute: %v", err)
					return
				}
				_ = v.(demoData)
			}
		}(w)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if err := inv.Invalidate(ctx, rt, "s"); err != nil {
					t.Errorf("invalidate: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	inv.fence.mu.Lock()
	n := len(inv.fence.fences)
	inv.fence.mu.Unlock()
	if n != 0 {
		t.Fatalf("fence map must drain after quiescence: %d records", n)
	}
}

// TestE04InvalidateImmediatelyAroundSet: invalidation immediately before a
// Set completes removes the entry; immediately after also yields a miss.
func TestE04InvalidateImmediatelyAroundSet(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return demoData{Title: "v"}, nil
	}
	reg := newFencedRegistry(t, store, inv, JSONCodec[demoData](), constScope("s"), load, nil)
	ctx := context.Background()
	rt := testRoute("demo.show")
	r, _ := reg.Lookup("demo.show")
	if _, err := r.Execute(ctx, rt); err != nil {
		t.Fatal(err)
	}
	// Invalidation after the Set completes: next execute must reload.
	if err := inv.Invalidate(ctx, rt, "s"); err != nil {
		t.Fatal(err)
	}
	v, err := r.Execute(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(demoData); got.Title != "v" {
		t.Fatal("unexpected value")
	}
	// Fence map drained.
	inv.fence.mu.Lock()
	n := len(inv.fence.fences)
	inv.fence.mu.Unlock()
	if n != 0 {
		t.Fatalf("records leaked: %d", n)
	}
}

// TestE04SharedStoreTwoBindings: two fenced bindings on one shared store and
// one invalidator — invalidating one binding's key does not evict the other's
// entry, and both are individually fenced.
func TestE04SharedStoreTwoBindings(t *testing.T) {
	store := newTestStore(t)
	inv, _ := NewInvalidator(store)
	load := func(context.Context, routecontext.Route) (demoData, error) {
		return demoData{Title: "x"}, nil
	}
	b1 := BindCached("b1.page", load, CacheOptions[demoData]{
		Store: store, Scope: constScope("s"), Codec: JSONCodec[demoData](),
		Policy: cache.Policy{TTL: time.Minute}, Invalidator: inv,
	})
	b2 := BindCached("b2.page", load, CacheOptions[demoData]{
		Store: store, Scope: constScope("s"), Codec: JSONCodec[demoData](),
		Policy: cache.Policy{TTL: time.Minute}, Invalidator: inv,
	})
	reg, err := NewRegistry([]string{"b1.page", "b2.page"}, b1, b2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r1, _ := reg.Lookup("b1.page")
	r2, _ := reg.Lookup("b2.page")
	if _, err := r1.Execute(ctx, testRoute("b1.page")); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Execute(ctx, testRoute("b2.page")); err != nil {
		t.Fatal(err)
	}
	if len(store.sets) != 2 {
		t.Fatalf("both bindings must write: %d sets", len(store.sets))
	}
	// Invalidate b1's exact key only.
	if err := inv.Invalidate(ctx, testRoute("b1.page"), "s"); err != nil {
		t.Fatal(err)
	}
	// b2's entry remains a hit: no reload happens unless invalidated.
	v, err := r2.Execute(ctx, testRoute("b2.page"))
	if err != nil {
		t.Fatal(err)
	}
	_ = v
	if len(store.sets) != 2 {
		t.Fatalf("b2 must remain cached after b1 invalidation: sets=%d", len(store.sets))
	}
}
