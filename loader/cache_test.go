package loader

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-meta/cache"
	"go-meta/routecontext"
	"go-meta/routeparams"
)

type testData struct {
	Name   string
	Values []string
}

func memory(t testing.TB) *cache.Memory {
	t.Helper()
	s, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 128, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func publicScope(context.Context, routecontext.Route) (string, bool, error) {
	return "public-v1", true, nil
}
func route(id, param string, query url.Values) routecontext.Route {
	return routecontext.Route{RouteID: id, Params: routeparams.NewParams(map[string]string{"id": param}), Query: routecontext.NewQuery(query)}
}
func registered[T any](t testing.TB, id string, fn Loader[T], opts CacheOptions[T]) Registered {
	t.Helper()
	r, err := NewRegistry([]string{id}, BindCached(id, fn, opts))
	if err != nil {
		t.Fatal(err)
	}
	b, ok := r.Lookup(id)
	if !ok {
		t.Fatal("missing binding")
	}
	return b
}

func TestCachedLoaderTypedIsolationAndKeys(t *testing.T) {
	store := memory(t)
	var calls atomic.Int64
	load := func(_ context.Context, r routecontext.Route) (*testData, error) {
		calls.Add(1)
		id, _ := r.Params.Get("id")
		return &testData{Name: id + ":" + strings.Join(r.Query.Values("tag"), ","), Values: []string{"original"}}, nil
	}
	type scopeKey struct{}
	scope := func(ctx context.Context, _ routecontext.Route) (string, bool, error) {
		s, _ := ctx.Value(scopeKey{}).(string)
		return s, s != "", nil
	}
	opts := CacheOptions[*testData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: scope, Codec: JSONCodec[*testData]()}
	a := registered(t, "posts/[id]/page.templ", load, opts)
	b := registered(t, "other/[id]/page.templ", load, opts)
	ctx := context.WithValue(context.Background(), scopeKey{}, "tenant-a")
	r := route("posts/[id]/page.templ", "雪/1", url.Values{"tag": {"go", "web"}})
	first, err := a.Execute(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	first.(*testData).Values[0] = "mutated"
	second, err := a.Execute(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if second.(*testData).Values[0] != "original" || calls.Load() != 1 {
		t.Fatal("hit did not restore an independent typed value")
	}
	variants := []struct {
		h     Registered
		ctx   context.Context
		route routecontext.Route
	}{
		{a, context.WithValue(context.Background(), scopeKey{}, "tenant-b"), r},
		{a, ctx, route(r.RouteID, "雪/2", r.Query.All())},
		{a, ctx, route(r.RouteID, "雪/1", url.Values{"tag": {"web", "go"}})},
		{a, ctx, route(r.RouteID, "雪/1", url.Values{"tag": {""}})},
		{a, ctx, route(r.RouteID, "雪/1", nil)},
		{b, ctx, route("other/[id]/page.templ", "雪/1", r.Query.All())},
	}
	for _, v := range variants {
		if _, err := v.h.Execute(v.ctx, v.route); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 7 {
		t.Fatalf("scope/route/query collisions: %d calls", calls.Load())
	}
	if _, err := a.Execute(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 9 {
		t.Fatal("bypass must execute every time")
	}
	k1, _ := LoaderCacheKey(r, "tenant-a")
	reordered := route(r.RouteID, "雪/1", url.Values{"tag": {"go", "web"}})
	k2, _ := LoaderCacheKey(reordered, "tenant-a")
	if k1 != k2 {
		t.Fatal("nondeterministic key")
	}
	if _, err := LoaderCacheKey(route(r.RouteID, strings.Repeat("x", cache.MaxKeyLength), nil), "tenant-a"); !errors.Is(err, cache.ErrInvalidKey) {
		t.Fatalf("oversize: %v", err)
	}
}

func TestCacheConfigAndFailures(t *testing.T) {
	store := memory(t)
	ctx := context.Background()
	r := route("r", "1", nil)
	opts := CacheOptions[testData]{Store: store, Policy: cache.Policy{TTL: time.Second}, Scope: publicScope, Codec: JSONCodec[testData]()}
	fn := func(context.Context, routecontext.Route) (testData, error) { return testData{Name: "ok"}, nil }
	bad := opts
	bad.Policy.TTL = 0
	if reg, err := NewRegistry([]string{"r"}, BindCached("r", fn, bad)); reg != nil || !errors.Is(err, cache.ErrInvalidPolicy) {
		t.Fatalf("bad config: %v", err)
	}
	bad = opts
	bad.Scope = nil
	if reg, err := NewRegistry([]string{"r"}, BindCached("r", fn, bad)); reg != nil || !errors.Is(err, ErrInvalidCacheConfig) {
		t.Fatalf("bad scope: %v", err)
	}
	var events []CacheEvent
	opts.OnEvent = func(e CacheEvent) { events = append(events, e) }
	h := registered(t, "r", fn, opts)
	key, err := LoaderCacheKey(r, "public-v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, key, cache.Entry{Value: []byte("invalid")}, opts.Policy); err != nil {
		t.Fatal(err)
	}
	got, err := h.Execute(ctx, r)
	if err != nil || got.(testData).Name != "ok" {
		t.Fatalf("corrupt recovery: %#v %v", got, err)
	}
	if _, err = h.Execute(ctx, r); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []CacheEvent{CacheDecodeError, CacheHit}) {
		t.Fatalf("events: %v", events)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = h.Execute(ctx, r)
	if err != nil || got.(testData).Name != "ok" {
		t.Fatalf("closed fallback: %#v %v", got, err)
	}
	if !reflect.DeepEqual(events[len(events)-4:], []CacheEvent{CacheClosed, CacheReadError, CacheClosed, CacheWriteError}) {
		t.Fatalf("closed events: %v", events)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := h.Execute(cancelled, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestLossyJSONAndErrorsAreNotCached(t *testing.T) {
	type lossy struct {
		Visible string
		hidden  string
	}
	store := memory(t)
	var calls int
	opts := CacheOptions[lossy]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[lossy]()}
	h := registered(t, "r", func(context.Context, routecontext.Route) (lossy, error) {
		calls++
		return lossy{Visible: "yes", hidden: "secret"}, nil
	}, opts)
	for range 2 {
		got, err := h.Execute(context.Background(), route("r", "", nil))
		if err != nil || got.(lossy).hidden != "secret" {
			t.Fatalf("lossy: %#v %v", got, err)
		}
	}
	if calls != 2 {
		t.Fatal("lossy JSON was cached")
	}
	for _, failure := range []error{ErrNotFound, Redirect("/"), errors.New("internal"), context.Canceled} {
		calls = 0
		fail := registered(t, "failure", func(context.Context, routecontext.Route) (lossy, error) { calls++; return lossy{}, failure }, opts)
		for range 2 {
			if _, err := fail.Execute(context.Background(), route("failure", "", nil)); !errors.Is(err, failure) {
				t.Fatal(err)
			}
		}
		if calls != 2 {
			t.Fatalf("cached failure: %v", failure)
		}
	}
}

func TestConcurrentMissesAndHits(t *testing.T) {
	store := memory(t)
	var calls atomic.Int64
	h := registered(t, "r", func(_ context.Context, r routecontext.Route) (testData, error) {
		calls.Add(1)
		id, _ := r.Params.Get("id")
		return testData{Name: id, Values: []string{"value"}}, nil
	}, CacheOptions[testData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[testData]()})
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			r := route("r", fmt.Sprint(i%4), nil)
			for range 10 {
				got, err := h.Execute(context.Background(), r)
				if err != nil || got.(testData).Name != fmt.Sprint(i%4) {
					t.Errorf("cross-request result: %#v %v", got, err)
					return
				}
				got.(testData).Values[0] = "private"
			}
		})
	}
	wg.Wait()
	if calls.Load() < 4 {
		t.Fatal("expected misses")
	}
}

type timedStore struct {
	now   time.Duration
	until time.Duration
	entry cache.Entry
	found bool
}

func (s *timedStore) Get(ctx context.Context, _ cache.Key) (cache.Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return cache.Entry{}, false, err
	}
	return s.entry, s.found && s.now < s.until, nil
}
func (s *timedStore) Set(ctx context.Context, _ cache.Key, entry cache.Entry, policy cache.Policy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.entry = cache.Entry{Value: append([]byte(nil), entry.Value...)}
	s.until = s.now + policy.TTL
	s.found = true
	return nil
}
func (s *timedStore) Delete(ctx context.Context, _ cache.Key) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.found = false
	return nil
}

func TestTTLExpiryAndNilValues(t *testing.T) {
	store := &timedStore{}
	calls := 0
	opts := CacheOptions[*testData]{Store: store, Policy: cache.Policy{TTL: time.Second}, Scope: publicScope, Codec: JSONCodec[*testData]()}
	h := registered(t, "nil", func(context.Context, routecontext.Route) (*testData, error) { calls++; return nil, nil }, opts)
	for range 2 {
		got, err := h.Execute(context.Background(), route("nil", "", nil))
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := got.(*testData); !ok || v != nil {
			t.Fatalf("nil pointer lost: %#v", got)
		}
	}
	if calls != 1 {
		t.Fatalf("nil hit: %d calls", calls)
	}
	store.now = time.Second
	if _, err := h.Execute(context.Background(), route("nil", "", nil)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expired entry was not reloaded: %d", calls)
	}
}

func TestEncodingErrorsAndCancellation(t *testing.T) {
	store := memory(t)
	calls := 0
	events := []CacheEvent{}
	codec := JSONCodec[testData]()
	codec.Encode = func(testData) ([]byte, error) { return nil, errors.New("private encoding detail") }
	opts := CacheOptions[testData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: codec, OnEvent: func(e CacheEvent) { events = append(events, e) }}
	h := registered(t, "r", func(context.Context, routecontext.Route) (testData, error) {
		calls++
		return testData{Name: "valid"}, nil
	}, opts)
	for range 2 {
		value, err := h.Execute(context.Background(), route("r", "", nil))
		if err != nil || value.(testData).Name != "valid" {
			t.Fatalf("encode fallback: %#v %v", value, err)
		}
	}
	if calls != 2 || !reflect.DeepEqual(events, []CacheEvent{CacheMiss, CacheEncodeError, CacheMiss, CacheEncodeError}) {
		t.Fatalf("calls=%d events=%v", calls, events)
	}
	ctx, cancel := context.WithCancel(context.Background())
	opts.Codec.Encode = func(v testData) ([]byte, error) { cancel(); return nil, errors.New("encode") }
	h = registered(t, "cancel", func(context.Context, routecontext.Route) (testData, error) { return testData{}, nil }, opts)
	if _, err := h.Execute(ctx, route("cancel", "", nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel during encode: %v", err)
	}
}

type namedValue string

type customJSON struct{ Label string }

func (v customJSON) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote("prefix:" + v.Label)), nil
}
func (v *customJSON) UnmarshalJSON(data []byte) error {
	s, err := strconv.Unquote(string(data))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(s, "prefix:") {
		return errors.New("bad prefix")
	}
	v.Label = strings.TrimPrefix(s, "prefix:")
	return nil
}

func assertTypedHit[T any](t *testing.T, value T) {
	t.Helper()
	store := memory(t)
	calls := 0
	h := registered(t, "typed", func(context.Context, routecontext.Route) (T, error) { calls++; return value, nil }, CacheOptions[T]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[T]()})
	for range 2 {
		got, err := h.Execute(context.Background(), route("typed", "x", nil))
		if err != nil {
			t.Fatal(err)
		}
		typed, ok := got.(T)
		if !ok || !reflect.DeepEqual(typed, value) {
			t.Fatalf("typed hit: %#v, %T", got, got)
		}
	}
	if calls != 1 {
		t.Fatalf("typed result was not cached: %d", calls)
	}
}

func TestJSONCodecTypedValues(t *testing.T) {
	assertTypedHit(t, namedValue("named"))
	assertTypedHit(t, []string{})
	assertTypedHit(t, []string(nil))
	assertTypedHit(t, customJSON{Label: "custom"})
	assertTypedHit(t, testData{Name: "nested", Values: []string{"x"}})
	assertTypedHit(t, 0)
}

func TestScopeErrorsAndInvalidKeysNeverExecuteLoader(t *testing.T) {
	store := memory(t)
	calls := 0
	opts := CacheOptions[int]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Codec: JSONCodec[int](), Scope: func(context.Context, routecontext.Route) (string, bool, error) { return "", true, nil }}
	h := registered(t, "r", func(context.Context, routecontext.Route) (int, error) { calls++; return 1, nil }, opts)
	if _, err := h.Execute(context.Background(), route("r", "x", nil)); !errors.Is(err, cache.ErrInvalidKey) {
		t.Fatalf("empty scope: %v", err)
	}
	opts.Scope = func(context.Context, routecontext.Route) (string, bool, error) {
		return "", false, errors.New("scope error")
	}
	h = registered(t, "r", func(context.Context, routecontext.Route) (int, error) { calls++; return 1, nil }, opts)
	if _, err := h.Execute(context.Background(), route("r", "x", nil)); err == nil {
		t.Fatal("scope error ignored")
	}
	if calls != 0 {
		t.Fatalf("unsafe fallback executed: %d", calls)
	}
}

func TestCodecDecodeCannotMutateBytesWrittenToStore(t *testing.T) {
	store := memory(t)
	codec := Codec[string]{
		Encode: func(value string) ([]byte, error) { return []byte(value), nil },
		Decode: func(data []byte) (string, error) {
			value := string(data)
			if len(data) > 0 {
				data[0] = '!'
			}
			return value, nil
		},
	}
	calls := 0
	h := registered(t, "codec", func(context.Context, routecontext.Route) (string, error) { calls++; return "original", nil }, CacheOptions[string]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: codec})
	for range 2 {
		value, err := h.Execute(context.Background(), route("codec", "", nil))
		if err != nil || value != "original" {
			t.Fatalf("codec hit: %#v %v", value, err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected cache hit, got %d calls", calls)
	}
}

func TestCachedLoaderReturnsCorrectDataForEachSecurityScope(t *testing.T) {
	type userKey struct{}
	store := memory(t)
	var calls atomic.Int32
	scope := func(ctx context.Context, _ routecontext.Route) (string, bool, error) {
		user, _ := ctx.Value(userKey{}).(string)
		return user, user != "", nil
	}
	load := func(ctx context.Context, _ routecontext.Route) (testData, error) {
		calls.Add(1)
		return testData{Name: ctx.Value(userKey{}).(string)}, nil
	}
	h := registered(t, "private", load, CacheOptions[testData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: scope, Codec: JSONCodec[testData]()})
	for _, user := range []string{"tenant-a", "tenant-b", "tenant-a", "tenant-b"} {
		ctx := context.WithValue(context.Background(), userKey{}, user)
		value, err := h.Execute(ctx, route("private", "same", url.Values{"tab": {"same"}}))
		if err != nil || value.(testData).Name != user {
			t.Fatalf("scope %s received %#v: %v", user, value, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("expected two independent entries, got %d loads", calls.Load())
	}
}
