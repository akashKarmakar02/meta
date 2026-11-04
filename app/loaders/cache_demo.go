package loaders

import (
	"context"
	"time"

	"go-meta/cache"
	"go-meta/loader"
	"go-meta/routecontext"
)

// CacheDemoData is public demonstration data; it contains no user information.
type CacheDemoData struct {
	ID        string `json:"id"`
	Tab       string `json:"tab"`
	Refreshed string `json:"refreshed"`
}

func loadCacheDemo(_ context.Context, route routecontext.Route) (CacheDemoData, error) {
	id, _ := route.Params.Get("id")
	return CacheDemoData{ID: id, Tab: route.Query.Get("tab"), Refreshed: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

// CachedDemoBindings opts only these public examples into the supplied store.
func CachedDemoBindings(store cache.Store) []loader.Binding {
	options := loader.CacheOptions[CacheDemoData]{
		Store:  store,
		Policy: cache.Policy{TTL: 2 * time.Second},
		Scope: func(context.Context, routecontext.Route) (string, bool, error) {
			return "go-meta-example-public-v1", true, nil
		},
		Codec: loader.JSONCodec[CacheDemoData](),
	}
	return []loader.Binding{
		loader.BindCached("cache-demo/post/[id]/page.templ", loadCacheDemo, options),
		loader.BindCached("cache-demo/team/[id]/page.svelte", loadCacheDemo, options),
	}
}
