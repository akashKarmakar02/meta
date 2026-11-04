package loader

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-meta/cache"
	"go-meta/routecontext"
)

type flightData struct {
	Value int
	Items []string
	Meta  map[string]string
}

func newFlightTest(t testing.TB, load Loader[flightData]) (*FlightGroup, *Invalidator, *recordingStore, Registered, routecontext.Route) {
	t.Helper()
	store := newTestStoreForFlight(t)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewFlightGroup(inv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	opts := CacheOptions[flightData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[flightData](), Invalidator: inv, Flights: group}
	return group, inv, store, registered(t, "flight", load, opts), route("flight", "42", nil)
}

func newTestStoreForFlight(t testing.TB) *recordingStore {
	t.Helper()
	m, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 1000, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return &recordingStore{inner: m}
}

func waitFlights(t testing.TB, g *FlightGroup, count, waiters int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		n, w := len(g.calls), 0
		for _, c := range g.calls {
			w += c.waiters
		}
		g.mu.Unlock()
		if n == count && w == waiters {
			return
		}
		runtime.Gosched()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	t.Fatalf("flight state: %d active, wanted %d flights and %d waiters", len(g.calls), count, waiters)
}

type result struct {
	value any
	err   error
}

func startFlight(ctx context.Context, h Registered, r routecontext.Route) <-chan result {
	out := make(chan result, 1)
	go func() { v, err := h.Execute(ctx, r); out <- result{v, err} }()
	return out
}
func receive(t testing.TB, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for flight")
		return result{}
	}
}

func TestFlightIndependentWaitersAndOwnership(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return flightData{Value: 7, Items: []string{"original"}, Meta: map[string]string{"key": "original"}}, nil
		case <-ctx.Done():
			return flightData{}, ctx.Err()
		}
	}
	group, _, _, h, r := newFlightTest(t, load)
	ctxA, cancelA := context.WithCancel(context.Background())
	a := startFlight(ctxA, h, r)
	<-entered
	b := startFlight(context.Background(), h, r)
	c := startFlight(context.Background(), h, r)
	waitFlights(t, group, 1, 3)
	cancelA()
	if got := receive(t, a); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("leader: %v", got.err)
	}
	close(release)
	rb, rc := receive(t, b), receive(t, c)
	if rb.err != nil || rc.err != nil {
		t.Fatalf("followers: %v %v", rb.err, rc.err)
	}
	vb, vc := rb.value.(flightData), rc.value.(flightData)
	vb.Items[0] = "changed"
	vb.Meta["key"] = "changed"
	if vc.Items[0] != "original" || vc.Meta["key"] != "original" {
		t.Fatal("shared mutable result")
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls: %d", calls.Load())
	}
	hit, err := h.Execute(context.Background(), r)
	if err != nil || hit.(flightData).Value != 7 || calls.Load() != 1 {
		t.Fatalf("cache hit: %#v %v", hit, err)
	}
	waitFlights(t, group, 0, 0)
}

func TestFlightInvalidationSeparatesGenerations(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var version atomic.Int32
	version.Store(1)
	var calls atomic.Int32
	load := func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		calls.Add(1)
		v := int(version.Load())
		if v == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return flightData{}, ctx.Err()
			}
		}
		return flightData{Value: v}, nil
	}
	group, inv, store, h, r := newFlightTest(t, load)
	a := startFlight(context.Background(), h, r)
	<-entered
	b := startFlight(context.Background(), h, r)
	waitFlights(t, group, 1, 2)
	version.Store(2)
	if err := inv.Invalidate(context.Background(), r, "public-v1"); err != nil {
		t.Fatal(err)
	}
	c := receive(t, startFlight(context.Background(), h, r))
	if c.err != nil || c.value.(flightData).Value != 2 {
		t.Fatalf("new generation: %#v %v", c.value, c.err)
	}
	close(release)
	ra, rb := receive(t, a), receive(t, b)
	if ra.err != nil || rb.err != nil || ra.value.(flightData).Value != 1 || rb.value.(flightData).Value != 1 {
		t.Fatalf("old generation: %#v %#v", ra, rb)
	}
	d, err := h.Execute(context.Background(), r)
	if err != nil || d.(flightData).Value != 2 || calls.Load() != 2 {
		t.Fatalf("stale cache: %#v %v calls=%d", d, err, calls.Load())
	}
	store.mu.Lock()
	sets := len(store.sets)
	store.mu.Unlock()
	if sets != 1 {
		t.Fatalf("sets=%d", sets)
	}
	waitFlights(t, group, 0, 0)
}

func TestFlightLastWaiterCancellationAndReplacement(t *testing.T) {
	entered, cancelled, releaseOld := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-releaseOld
			return flightData{Value: 1}, nil
		}
		return flightData{Value: 2}, nil
	}
	group, inv, _, h, r := newFlightTest(t, load)
	ca, cancelA := context.WithCancel(context.Background())
	cb, cancelB := context.WithCancel(context.Background())
	a := startFlight(ca, h, r)
	<-entered
	b := startFlight(cb, h, r)
	waitFlights(t, group, 1, 2)
	cancelA()
	cancelB()
	if !errors.Is(receive(t, a).err, context.Canceled) || !errors.Is(receive(t, b).err, context.Canceled) {
		t.Fatal("waiters did not cancel")
	}
	<-cancelled
	waitFlights(t, group, 0, 0)
	next := receive(t, startFlight(context.Background(), h, r))
	if next.err != nil || next.value.(flightData).Value != 2 {
		t.Fatalf("replacement: %#v", next)
	}
	close(releaseOld)
	deadline := time.Now().Add(5 * time.Second)
	for {
		inv.fence.mu.Lock()
		remaining := len(inv.fence.fences)
		inv.fence.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("abandoned worker did not release its fence")
		}
		runtime.Gosched()
	}
	got, err := h.Execute(context.Background(), r)
	if err != nil || got.(flightData).Value != 2 {
		t.Fatalf("abandoned write: %#v %v", got, err)
	}
}

func TestFlightPanicWakesWaitersAndCleansUp(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context, routecontext.Route) (flightData, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			panic("secret")
		}
		return flightData{Value: 3}, nil
	}
	group, _, _, h, r := newFlightTest(t, load)
	a := startFlight(context.Background(), h, r)
	<-entered
	b := startFlight(context.Background(), h, r)
	waitFlights(t, group, 1, 2)
	close(release)
	if !errors.Is(receive(t, a).err, ErrFlightPanic) || !errors.Is(receive(t, b).err, ErrFlightPanic) {
		t.Fatal("panic did not reach waiters")
	}
	waitFlights(t, group, 0, 0)
	got, err := h.Execute(context.Background(), r)
	if err != nil || got.(flightData).Value != 3 {
		t.Fatalf("retry: %#v %v", got, err)
	}
}

func TestFlightShortDeadlineDoesNotCancelFollower(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	group, _, _, h, r := newFlightTest(t, func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		close(entered)
		select {
		case <-release:
			return flightData{Value: 9}, nil
		case <-ctx.Done():
			return flightData{}, ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	a := startFlight(ctx, h, r)
	<-entered
	b := startFlight(context.Background(), h, r)
	waitFlights(t, group, 1, 2)
	if !errors.Is(receive(t, a).err, context.DeadlineExceeded) {
		t.Fatal("deadline not preserved")
	}
	close(release)
	got := receive(t, b)
	if got.err != nil || got.value.(flightData).Value != 9 {
		t.Fatalf("follower: %#v", got)
	}
}

func TestFlightContextDoesNotInheritRequestValues(t *testing.T) {
	type contextKey struct{}
	group, _, _, h, r := newFlightTest(t, func(ctx context.Context, route routecontext.Route) (flightData, error) {
		if ctx.Value(contextKey{}) != nil {
			return flightData{}, errors.New("request value leaked")
		}
		snapshot, ok := routecontext.FromContext(ctx)
		if !ok || snapshot.RouteID != route.RouteID {
			return flightData{}, errors.New("route missing")
		}
		return flightData{Value: 1}, nil
	})
	ctx := context.WithValue(context.Background(), contextKey{}, "private")
	got, err := h.Execute(ctx, r)
	if err != nil || got.(flightData).Value != 1 {
		t.Fatalf("context: %#v %v", got, err)
	}
	waitFlights(t, group, 0, 0)
}

func TestFlightConfigurationValidation(t *testing.T) {
	store := newTestStoreForFlight(t)
	inv, _ := NewInvalidator(store)
	other, _ := NewInvalidator(store)
	if _, err := NewFlightGroup(inv, 0); !errors.Is(err, ErrInvalidCacheConfig) {
		t.Fatalf("timeout: %v", err)
	}
	group, err := NewFlightGroup(inv, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	opts := CacheOptions[flightData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[flightData](), Invalidator: other, Flights: group}
	load := func(context.Context, routecontext.Route) (flightData, error) { return flightData{}, nil }
	if _, err := NewRegistry([]string{"flight"}, BindCached("flight", load, opts)); !errors.Is(err, ErrInvalidCacheConfig) {
		t.Fatalf("mismatch: %v", err)
	}
}

func TestFlightConcurrentUniqueKeysCleanup(t *testing.T) {
	group, inv, _, h, _ := newFlightTest(t, func(context.Context, routecontext.Route) (flightData, error) { return flightData{Value: 1}, nil })
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			r := route("flight", fmt.Sprint(i), nil)
			if _, err := h.Execute(context.Background(), r); err != nil {
				t.Error(err)
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
		runtime.Gosched()
	}
	t.Fatal("fence records retained")
}

func TestFlightErrorsAndRedirectAreSharedButNotCached(t *testing.T) {
	for _, failure := range []error{fmt.Errorf("wrapped: %w", ErrNotFound), Redirect("/about"), errors.New("internal")} {
		t.Run(failure.Error(), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			group, _, _, h, r := newFlightTest(t, func(context.Context, routecontext.Route) (flightData, error) {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
				}
				return flightData{}, failure
			})
			a := startFlight(context.Background(), h, r)
			<-entered
			b := startFlight(context.Background(), h, r)
			waitFlights(t, group, 1, 2)
			close(release)
			for _, ch := range []<-chan result{a, b} {
				if got := receive(t, ch); !errors.Is(got.err, failure) {
					t.Fatalf("error: %v", got.err)
				}
			}
			if _, err := h.Execute(context.Background(), r); !errors.Is(err, failure) || calls.Load() != 2 {
				t.Fatalf("failure cached or changed: %v calls=%d", err, calls.Load())
			}
			waitFlights(t, group, 0, 0)
		})
	}
}

func TestFlightCodecFailurePreservesEachRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	store := newTestStoreForFlight(t)
	inv, _ := NewInvalidator(store)
	group, _ := NewFlightGroup(inv, time.Second)
	codec := JSONCodec[flightData]()
	codec.Encode = func(flightData) ([]byte, error) { return nil, errors.New("codec failed") }
	load := func(context.Context, routecontext.Route) (flightData, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return flightData{Value: 42}, nil
	}
	opts := CacheOptions[flightData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: codec, Invalidator: inv, Flights: group}
	h := registered(t, "flight", load, opts)
	r := route("flight", "42", nil)
	a := startFlight(context.Background(), h, r)
	<-entered
	b := startFlight(context.Background(), h, r)
	waitFlights(t, group, 1, 2)
	close(release)
	for _, ch := range []<-chan result{a, b} {
		got := receive(t, ch)
		if got.err != nil || got.value.(flightData).Value != 42 {
			t.Fatalf("codec fallback: %#v", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("each request must receive its own unshareable result: calls=%d", calls.Load())
	}
}

func TestFlightDifferentKeysProgressIndependently(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	group, _, _, h, r := newFlightTest(t, func(ctx context.Context, r routecontext.Route) (flightData, error) {
		calls.Add(1)
		id, _ := r.Params.Get("id")
		if id == "42" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return flightData{}, ctx.Err()
			}
		}
		return flightData{Value: len(id)}, nil
	})
	a := startFlight(context.Background(), h, r)
	<-entered
	other := route("flight", "different", nil)
	b := receive(t, startFlight(context.Background(), h, other))
	if b.err != nil || b.value.(flightData).Value != len("different") {
		t.Fatalf("independent key: %#v", b)
	}
	close(release)
	if got := receive(t, a); got.err != nil {
		t.Fatal(got.err)
	}
	waitFlights(t, group, 0, 0)
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestFlightTimeoutStopsSharedWork(t *testing.T) {
	store := newTestStoreForFlight(t)
	inv, _ := NewInvalidator(store)
	group, _ := NewFlightGroup(inv, 30*time.Millisecond)
	ended := make(chan struct{})
	var calls atomic.Int32
	h := registered(t, "flight", func(ctx context.Context, _ routecontext.Route) (flightData, error) {
		calls.Add(1)
		<-ctx.Done()
		close(ended)
		return flightData{}, ctx.Err()
	}, CacheOptions[flightData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[flightData](), Invalidator: inv, Flights: group})
	if _, err := h.Execute(context.Background(), route("flight", "42", nil)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	<-ended
	waitFlights(t, group, 0, 0)
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestFlightFailedInvalidationThenRetrySeparatesWork(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var version atomic.Int32
	version.Store(1)
	group, inv, store, h, r := newFlightTest(t, func(_ context.Context, _ routecontext.Route) (flightData, error) {
		v := int(version.Load())
		if v == 1 {
			close(entered)
			<-release
		}
		return flightData{Value: v}, nil
	})
	old := startFlight(context.Background(), h, r)
	<-entered
	store.failDel.Store(true)
	version.Store(2)
	if err := inv.Invalidate(context.Background(), r, "public-v1"); err == nil {
		t.Fatal("expected delete failure")
	}
	store.failDel.Store(false)
	if err := inv.Invalidate(context.Background(), r, "public-v1"); err != nil {
		t.Fatal(err)
	}
	fresh := receive(t, startFlight(context.Background(), h, r))
	if fresh.err != nil || fresh.value.(flightData).Value != 2 {
		t.Fatalf("fresh: %#v", fresh)
	}
	close(release)
	if oldResult := receive(t, old); oldResult.err != nil || oldResult.value.(flightData).Value != 1 {
		t.Fatalf("old: %#v", oldResult)
	}
	got, err := h.Execute(context.Background(), r)
	if err != nil || got.(flightData).Value != 2 {
		t.Fatalf("stale cache: %#v %v", got, err)
	}
	waitFlights(t, group, 0, 0)
}

func TestFlightTimeoutReleasesWaiterEvenIfLoaderIgnoresCancellation(t *testing.T) {
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	load := func(context.Context, routecontext.Route) (flightData, error) {
		close(entered)
		<-release
		close(exited)
		return flightData{Value: 1}, nil
	}
	store := newTestStoreForFlight(t)
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewFlightGroup(inv, 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	h := registered(t, "flight", load, CacheOptions[flightData]{Store: store, Invalidator: inv, Flights: group, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[flightData]()})
	defer func() { close(release); <-exited }()
	r := startFlight(context.Background(), h, route("flight", "1", nil))
	<-entered
	if got := receive(t, r); !errors.Is(got.err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", got.err)
	}
	waitFlights(t, group, 0, 0)
}

func TestFlightScopesRemainIndependent(t *testing.T) {
	entered, release := make(chan string, 2), make(chan struct{})
	type scopeKey struct{}
	store := newTestStoreForFlight(t)
	inv, _ := NewInvalidator(store)
	group, _ := NewFlightGroup(inv, time.Second*5)
	scope := func(ctx context.Context, _ routecontext.Route) (string, bool, error) {
		return ctx.Value(scopeKey{}).(string), true, nil
	}
	load := func(_ context.Context, r routecontext.Route) (flightData, error) {
		id, _ := r.Params.Get("id")
		entered <- id
		<-release
		return flightData{Value: 1}, nil
	}
	h := registered(t, "flight", load, CacheOptions[flightData]{Store: store, Invalidator: inv, Flights: group, Policy: cache.Policy{TTL: time.Minute}, Scope: scope, Codec: JSONCodec[flightData]()})
	r := route("flight", "42", nil)
	a := startFlight(context.WithValue(context.Background(), scopeKey{}, "tenant-a"), h, r)
	b := startFlight(context.WithValue(context.Background(), scopeKey{}, "tenant-b"), h, r)
	<-entered
	<-entered
	waitFlights(t, group, 2, 2)
	close(release)
	if x, y := receive(t, a), receive(t, b); x.err != nil || y.err != nil {
		t.Fatalf("%v %v", x.err, y.err)
	}
}

type storeWithUncomparableValue struct {
	cache.Store
	extra any
}

func TestFlightRejectsUncomparableStoreWithoutPanic(t *testing.T) {
	underlying := newTestStoreForFlight(t)
	store := storeWithUncomparableValue{Store: underlying, extra: []int{1}}
	inv, err := NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := NewFlightGroup(inv, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	binding := BindCached("flight", func(context.Context, routecontext.Route) (flightData, error) { return flightData{}, nil }, CacheOptions[flightData]{Store: store, Invalidator: inv, Flights: group, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[flightData]()})
	if _, err := NewRegistry([]string{"flight"}, binding); !errors.Is(err, ErrInvalidCacheConfig) {
		t.Fatalf("expected invalid configuration, got %v", err)
	}
}
