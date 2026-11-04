package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-meta/action"
	appactions "go-meta/app/actions"
	apploaders "go-meta/app/loaders"
	"go-meta/cache"
	"go-meta/loader"
	"go-meta/routecontext"
)

// TestE04ActionInvalidatesCachedSSRAndCSR drives the full loop: load (cache
// fill), mutate through the real action endpoint, verify the next SSR and CSR
// requests reload fresh committed data. Uses the same binding/store/
// invalidator configuration as the example application.
func TestE04ActionInvalidatesCachedSSRAndCSR(t *testing.T) {
	appactions.Reset()
	appactions.DemoInvalidator = nil
	store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 64, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inv, err := loader.NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	var loads atomic.Int32
	load := func(_ context.Context, r routecontext.Route) (apploaders.CacheDemoData, error) {
		loads.Add(1)
		id, _ := r.Params.Get("id")
		if v, ok := apploaders.DemoVersion(id); ok {
			return apploaders.CacheDemoData{ID: id, Tab: r.Query.Get("tab"), Refreshed: v}, nil
		}
		return apploaders.CacheDemoData{ID: id, Tab: r.Query.Get("tab"), Refreshed: "V0"}, nil
	}
	scope := func(context.Context, routecontext.Route) (string, bool, error) {
		return "go-meta-example-public-v1", true, nil
	}
	options := loader.CacheOptions[apploaders.CacheDemoData]{
		Store:       store,
		Policy:      cache.Policy{TTL: time.Minute},
		Scope:       scope,
		Codec:       loader.JSONCodec[apploaders.CacheDemoData](),
		Invalidator: inv,
	}
	reg, err := newLoaderRegistry([]loader.Binding{
		loader.BindCached("cache-demo/post/[id]/page.templ", load, options),
		loader.BindCached("cache-demo/team/[id]/page.svelte", load, options),
	})
	if err != nil {
		t.Fatal(err)
	}
	appactions.DemoInvalidator = inv
	t.Cleanup(func() { appactions.DemoInvalidator = nil })
	areg, err := action.NewRegistry(appactions.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredAppWithActions(nil, false, reg, areg, d02TrustedOrigin, nil)

	get := func(url string, nav bool) (int, string) {
		return getBody(t, app, "e04", url, nav)
	}
	// 1. Fill both caches (SSR direct + CSR soft-nav envelope).
	if code, body := get("/cache-demo/post/7", false); code != 200 || !strings.Contains(body, "V0") {
		t.Fatalf("SSR fill: %d %s", code, body[:min(len(body), 300)])
	}
	if code, body := get("/cache-demo/team/7", true); code != 200 || !strings.Contains(body, "V0") {
		t.Fatalf("CSR fill: %d %s", code, body[:min(len(body), 300)])
	}
	if loads.Load() != 2 {
		t.Fatalf("expected 2 loads, got %d", loads.Load())
	}
	// 2. Cache hits — no additional loads.
	get("/cache-demo/post/7", false)
	get("/cache-demo/team/7", true)
	if loads.Load() != 2 {
		t.Fatalf("cache hits failed: %d loads", loads.Load())
	}
	// 3. Mutate through the action endpoint (JSON transport, trusted origin).
	acode, abody, _ := postAction(app, "/_meta/actions/cache-demo.update", d02TrustedOrigin, "application/json",
		`{"id":"7","title":"V2"}`)
	if acode != 200 {
		t.Fatalf("update action: %d %s", acode, abody)
	}
	// 4. Next requests must show the committed V2 through fresh loads.
	if code, body := get("/cache-demo/post/7", false); code != 200 || !strings.Contains(body, "V2") {
		t.Fatalf("post-invalidation SSR: %d %s", code, body[:min(len(body), 300)])
	}
	if code, body := get("/cache-demo/team/7", true); code != 200 || !strings.Contains(body, "V2") {
		t.Fatalf("post-invalidation CSR: %d %s", code, body[:min(len(body), 300)])
	}
	if loads.Load() != 4 {
		t.Fatalf("expected exactly 4 loads (2 fresh after invalidation), got %d", loads.Load())
	}
	// 5. Exact-key semantics: the action invalidated the tabless identity,
	//    not every query combination. The ?tab=x variant is a distinct key
	//    (query is part of E03 identity) whose first fill happens now —
	//    after the mutation — so it loads fresh and observes V2, proving both
	//    exact-key isolation (its own entry) and mutation visibility.
	if code, body := get("/cache-demo/team/7?tab=x", true); code != 200 || !strings.Contains(body, "V2") {
		t.Fatalf("query variant: %d %s", code, body[:min(len(body), 300)])
	}
	if loads.Load() != 5 {
		t.Fatalf("query variant should be its own fresh load, got %d loads", loads.Load())
	}
	// 6. Parameter isolation: a different id was never mutated or invalidated;
	//    its first load still reads the untouched default V0.
	if code, body := get("/cache-demo/post/8", false); code != 200 || !strings.Contains(body, "V0") {
		t.Fatalf("other id: %d %s", code, body[:min(len(body), 300)])
	}
}

// TestE04FailedActionDoesNotInvalidate: an invalid action submission does not
// invalidate anything — the cached entry survives a rejected mutation.
func TestE04FailedActionDoesNotInvalidate(t *testing.T) {
	appactions.Reset()
	appactions.DemoInvalidator = nil
	store, _ := cache.NewMemory(cache.MemoryConfig{MaxEntries: 64, MaxBytes: 1 << 20})
	defer store.Close()
	inv, _ := loader.NewInvalidator(store)
	var loads atomic.Int32
	load := func(_ context.Context, r routecontext.Route) (apploaders.CacheDemoData, error) {
		loads.Add(1)
		id, _ := r.Params.Get("id")
		return apploaders.CacheDemoData{ID: id, Refreshed: "V0"}, nil
	}
	options := loader.CacheOptions[apploaders.CacheDemoData]{
		Store: store, Policy: cache.Policy{TTL: time.Minute},
		Scope: func(context.Context, routecontext.Route) (string, bool, error) {
			return "go-meta-example-public-v1", true, nil
		},
		Codec:       loader.JSONCodec[apploaders.CacheDemoData](),
		Invalidator: inv,
	}
	reg, _ := newLoaderRegistry([]loader.Binding{
		loader.BindCached("cache-demo/post/[id]/page.templ", load, options),
	})
	appactions.DemoInvalidator = inv
	t.Cleanup(func() { appactions.DemoInvalidator = nil })
	areg, err := action.NewRegistry(appactions.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredAppWithActions(nil, false, reg, areg, d02TrustedOrigin, nil)
	get := func(url string) (int, string) { return getBody(t, app, "e04b", url, false) }
	if code, body := get("/cache-demo/post/9"); code != 200 || !strings.Contains(body, "V0") {
		t.Fatalf("fill: %d %s", code, body)
	}
	before := loads.Load()
	// Invalid action (empty id) — rejected, nothing invalidated.
	fcode, fbody, _ := postAction(app, "/_meta/actions/cache-demo.update", d02TrustedOrigin, "application/json", `{"title":"no id"}`)
	if fcode == 200 {
		t.Fatalf("invalid action unexpectedly succeeded: %s", fbody)
	}
	// The cached entry must still be served without a reload.
	if code, b := get("/cache-demo/post/9"); code != 200 || !strings.Contains(b, "V0") {
		t.Fatalf("cached entry lost after failed action: %d %s", code, b[:min(len(b), 200)])
	}
	if loads.Load() != before {
		t.Fatalf("failed action caused reload: %d -> %d", before, loads.Load())
	}
}

// TestE04ConcurrentSlowFillAndAction drives the §29 race over real HTTP: a
// slow fill reads V1 and pauses before its Set; the action commits V2 and
// invalidates; the fill resumes. A subsequent request must observe V2 — the
// fill's stale write was fenced off. The pause is deterministic: the loader
// blocks on a channel (no sleeps).
func TestE04ConcurrentSlowFillAndAction(t *testing.T) {
	appactions.Reset()
	appactions.DemoInvalidator = nil
	store, _ := cache.NewMemory(cache.MemoryConfig{MaxEntries: 64, MaxBytes: 1 << 20})
	defer store.Close()
	inv, _ := loader.NewInvalidator(store)
	gate := make(chan struct{})
	started := make(chan struct{})
	var loadedOnce sync.Once
	load := func(_ context.Context, r routecontext.Route) (apploaders.CacheDemoData, error) {
		id, _ := r.Params.Get("id")
		v := "V0"
		if d, ok := apploaders.DemoVersion(id); ok {
			v = d
		}
		if v == "V0" {
			// First (slow) fill parks inside the loader until released.
			loadedOnce.Do(func() { close(started) })
			select {
			case <-gate:
			case <-time.After(10 * time.Second):
				return apploaders.CacheDemoData{}, fmt.Errorf("test timeout")
			}
		}
		return apploaders.CacheDemoData{ID: id, Refreshed: v}, nil
	}
	options := loader.CacheOptions[apploaders.CacheDemoData]{
		Store:  store,
		Policy: cache.Policy{TTL: time.Minute},
		Scope: func(context.Context, routecontext.Route) (string, bool, error) {
			return "go-meta-example-public-v1", true, nil
		},
		Codec:       loader.JSONCodec[apploaders.CacheDemoData](),
		Invalidator: inv,
	}
	reg, err := newLoaderRegistry([]loader.Binding{
		loader.BindCached("cache-demo/post/[id]/page.templ", load, options),
	})
	if err != nil {
		t.Fatal(err)
	}
	appactions.DemoInvalidator = inv
	t.Cleanup(func() { appactions.DemoInvalidator = nil })
	areg, err := action.NewRegistry(appactions.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredAppWithActions(nil, false, reg, areg, d02TrustedOrigin, nil)
	get := func(url string) (int, string) { return getBody(t, app, "e04c", url, false) }

	// Request A starts; its loader parks after reading V0.
	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		code, body := get("/cache-demo/post/5")
		if code != 200 {
			t.Errorf("request A: %d %s", code, body)
		}
	}()
	<-started
	// Action commits V2 for id 5 and invalidates through the coordinator.
	apploaders.SetDemoVersion("5", "V2")
	if mcode, mbody, _ := postAction(app, "/_meta/actions/cache-demo.update", d02TrustedOrigin, "application/json",
		`{"id":"5","title":"V2"}`); mcode != 200 {
		t.Fatalf("update action: %d %s", mcode, mbody)
	}
	// Resume the parked fill; its write must be fenced off.
	close(gate)
	<-aDone
	// The next request must observe V2 (fresh load or valid post-invalidation
	// fill), never the stale V0 written by request A.
	code, body := get("/cache-demo/post/5")
	if code != 200 || !strings.Contains(body, "V2") {
		t.Fatalf("post-race request received stale data: %d %s", code, firstN(body, 300))
	}
	if strings.Count(body, "V0") > 0 && !strings.Contains(body, "V2") {
		t.Fatal("stale V0 served")
	}
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
