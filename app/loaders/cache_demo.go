package loaders

import (
	"context"
	"sync"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
)

// CacheDemoData is public demonstration data; it contains no user information.
type CacheDemoData struct {
	ID        string `json:"id"`
	Tab       string `json:"tab"`
	Refreshed string `json:"refreshed"`
}

func loadCacheDemo(ctx context.Context, route routecontext.Route) (CacheDemoData, error) {
	// A bounded delay makes concurrent cache fills observable in this demo.
	if route.Query.Get("slow") == "1" {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return CacheDemoData{}, ctx.Err()
		}
	}
	id, _ := route.Params.Get("id")
	refreshed := time.Now().UTC().Format(time.RFC3339Nano)
	if d, ok := demoVersion.Load(id); ok {
		// E04: after an invalidation the loader reads the committed mutation.
		refreshed = d.(string)
	}
	return CacheDemoData{ID: id, Tab: route.Query.Get("tab"), Refreshed: refreshed}, nil
}

// demoVersion holds post-mutation values set by the cache-demo.update action;
// it makes "fresh after invalidation" observable: a reloaded loader reads the
// committed value instead of the cached timestamp.
var demoVersion sync.Map

// SetDemoVersion commits a mutation for id (the cache-demo.update action).
func SetDemoVersion(id, value string) {
	demoVersion.Store(id, value)
}

// DemoVersion returns the committed value for id, if any.
func DemoVersion(id string) (string, bool) {
	v, ok := demoVersion.Load(id)
	if !ok {
		return "", false
	}
	return v.(string), true
}

// CachedDemoBindings opts only these public examples into the supplied store.
// The invalidator (E04) fences fills against coordinated invalidations; every
// cached binding over this store attaches it.
func CachedDemoBindings(store cache.Store, invalidator *loader.Invalidator) []loader.Binding {
	return cachedDemoBindings(store, invalidator, nil)
}

// CachedDemoBindingsWithFlights also opts the demonstration bindings into E05.
func CachedDemoBindingsWithFlights(store cache.Store, invalidator *loader.Invalidator, group *loader.FlightGroup) []loader.Binding {
	return cachedDemoBindings(store, invalidator, group)
}

func cachedDemoBindings(store cache.Store, invalidator *loader.Invalidator, group *loader.FlightGroup) []loader.Binding {
	options := loader.CacheOptions[CacheDemoData]{
		Store:  store,
		Policy: cache.Policy{TTL: 2 * time.Second},
		Scope: func(context.Context, routecontext.Route) (string, bool, error) {
			return "go-meta-example-public-v1", true, nil
		},
		Codec:       loader.JSONCodec[CacheDemoData](),
		Invalidator: invalidator,
	}
	options.Flights = group
	return []loader.Binding{
		loader.BindCached("cache-demo/post/[id]/page.templ", loadCacheDemo, options),
		loader.BindCached("cache-demo/team/[id]/page.svelte", loadCacheDemo, options),
	}
}
