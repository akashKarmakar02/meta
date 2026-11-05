package main

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apploaders "github.com/akashKarmakar02/meta/app/loaders"
	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
)

func TestE05ConcurrentHTTPFlights(t *testing.T) {
	for _, tc := range []struct {
		id, path string
		nav      bool
	}{
		{"cache-demo/post/[id]/page.templ", "/cache-demo/post/123", false},
		{"cache-demo/team/[id]/page.svelte", "/cache-demo/team/123", true},
	} {
		t.Run(tc.id, func(t *testing.T) {
			store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16, MaxBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			inv, err := loader.NewInvalidator(store)
			if err != nil {
				t.Fatal(err)
			}
			group, err := loader.NewFlightGroup(inv, time.Second*5)
			if err != nil {
				t.Fatal(err)
			}
			entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
			var calls atomic.Int32
			load := func(ctx context.Context, r routecontext.Route) (apploaders.CacheDemoData, error) {
				if calls.Add(1) == 1 {
					close(entered)
				}
				select {
				case <-release:
				case <-ctx.Done():
					return apploaders.CacheDemoData{}, ctx.Err()
				}
				return apploaders.CacheDemoData{ID: "123", Refreshed: "E05-unique-value"}, nil
			}
			opts := loader.CacheOptions[apploaders.CacheDemoData]{Store: store, Invalidator: inv, Flights: group, Policy: cache.Policy{TTL: time.Minute}, Codec: loader.JSONCodec[apploaders.CacheDemoData](), Scope: func(context.Context, routecontext.Route) (string, bool, error) { return "public", true, nil }, OnEvent: func(e loader.CacheEvent) {
				if e == loader.CacheFlightJoined {
					joined <- struct{}{}
				}
			}}
			reg, err := newLoaderRegistry([]loader.Binding{loader.BindCached(tc.id, load, opts)})
			if err != nil {
				t.Fatal(err)
			}
			app := newConfiguredApp(nil, false, reg)
			type response struct {
				code int
				body string
			}
			results := make(chan response, 2)
			go func() { code, body := getBody(t, app, "e05", tc.path, false); results <- response{code, body} }()
			<-entered
			go func() { code, body := getBody(t, app, "e05", tc.path, true); results <- response{code, body} }()
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Fatal("second request did not join flight")
			}
			close(release)
			for range 2 {
				r := <-results
				if r.code != 200 || !strings.Contains(r.body, "E05-unique-value") {
					t.Fatalf("bad response: %d %s", r.code, r.body)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("loader invoked %d times", calls.Load())
			}
			code, body := getBody(t, app, "e05", tc.path, tc.nav)
			if code != 200 || !strings.Contains(body, "E05-unique-value") || calls.Load() != 1 {
				t.Fatal("cache hit failed")
			}
		})
	}
}

func TestE05HTTPInvalidationSeparatesActiveFlights(t *testing.T) {
	store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inv, err := loader.NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := loader.NewFlightGroup(inv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var version, calls atomic.Int32
	version.Store(1)
	load := func(ctx context.Context, r routecontext.Route) (apploaders.CacheDemoData, error) {
		v := version.Load()
		calls.Add(1)
		if v == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return apploaders.CacheDemoData{}, ctx.Err()
			}
		}
		return apploaders.CacheDemoData{ID: "42", Refreshed: fmt.Sprintf("version-%d", v)}, nil
	}
	opts := loader.CacheOptions[apploaders.CacheDemoData]{Store: store, Invalidator: inv, Flights: group, Policy: cache.Policy{TTL: time.Minute}, Codec: loader.JSONCodec[apploaders.CacheDemoData](), Scope: func(context.Context, routecontext.Route) (string, bool, error) { return "public", true, nil }, OnEvent: func(e loader.CacheEvent) {
		if e == loader.CacheFlightJoined {
			joined <- struct{}{}
		}
	}}
	reg, err := newLoaderRegistry([]loader.Binding{loader.BindCached("cache-demo/post/[id]/page.templ", load, opts)})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, reg)
	responses := make(chan string, 2)
	go func() { _, body := getBody(t, app, "e05", "/cache-demo/post/42", false); responses <- body }()
	<-entered
	go func() { _, body := getBody(t, app, "e05", "/cache-demo/post/42", true); responses <- body }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("old follower did not join")
	}
	version.Store(2)
	r := routecontext.Route{RouteID: "cache-demo/post/[id]/page.templ", Params: routeparams.NewParams(map[string]string{"id": "42"})}
	if err := inv.Invalidate(context.Background(), r, "public"); err != nil {
		t.Fatal(err)
	}
	code, fresh := getBody(t, app, "e05", "/cache-demo/post/42", false)
	if code != 200 || !strings.Contains(fresh, "version-2") {
		t.Fatalf("new flight: %d %s", code, fresh)
	}
	close(release)
	for range 2 {
		if body := <-responses; !strings.Contains(body, "version-1") {
			t.Fatalf("old flight: %s", body)
		}
	}
	code, fresh = getBody(t, app, "e05", "/cache-demo/post/42", false)
	if code != 200 || !strings.Contains(fresh, "version-2") || calls.Load() != 2 {
		t.Fatalf("stale cache or extra load: %d calls=%d", code, calls.Load())
	}
}
