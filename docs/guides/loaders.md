# Load data for a page

A loader is a Go function that reads data for a route before rendering. Register it against the source path relative to `app/`, not the URL. For example, `app/about/page.templ` has the route ID `about/page.templ`.

Create `app/loaders/about.go`:

```go
package loaders

import (
    "context"

    "github.com/akashKarmakar02/meta/routecontext"
)

type AboutData struct {
    Greeting string `json:"greeting"`
}

func About(_ context.Context, _ routecontext.Route) (AboutData, error) {
    return AboutData{Greeting: "Hello from Go"}, nil
}
```

Add imports for your package and `github.com/akashKarmakar02/meta/loader` to `cmd/app/main.go`. Add this field to the existing `server.Config`:

```go
Loaders: []loader.Binding{
    loader.Bind("about/page.templ", loaders.About),
},
```

In `app/about/page.templ`, read the exact Go type returned by the loader. Change the module path to match your `go.mod`:

```templ
package about

import "example.com/my-app/app/loaders"
import "github.com/akashKarmakar02/meta/loaderdata"

templ About() {
    {{ data, ok := loaderdata.Get[loaders.AboutData](ctx) }}
    <main>
        <h1>About</h1>
        if ok {
            <p>{ data.Greeting }</p>
        }
    </main>
}
```

Meta runs the loader for a matching page request before rendering. Handle route parameters through `route.Params` in the loader. For a Svelte route, register its `page.svelte` route ID instead. Meta JSON-serializes the result and passes it to the page as a `data` prop, so only return data that the browser may see.

Loaders should read data, not perform mutations. Return the public `loader` package's error sentinels or `loader.Redirect` where appropriate. Do not assume client disconnects cancel a loader; pass the supplied context to operations that support cancellation. See the [loader reference](../../README.md#executing-csr-loaders-c04) for CSR props and error behavior.

## Cache a loader

`loader.BindCached` opts a typed loader into caching. It needs a `cache.Store`, a `Scope` function, and a `Codec`:

```go
store := cache.NewMemory(cache.MemoryConfig{MaxBytes: 1 << 20})
invalidator, err := loader.NewInvalidator(store)
if err != nil { log.Fatal(err) }

options := loader.CacheOptions[AboutData]{
    Store:  store,
    Policy: cache.Policy{TTL: time.Minute},
    Scope: func(context.Context, routecontext.Route) (string, bool, error) {
        // Same scope string shares entries; return false to skip caching.
        return "about-v1", true, nil
    },
    Codec:       loader.JSONCodec[AboutData](),
    Invalidator: invalidator,
}

Loaders: []loader.Binding{
    loader.BindCached("about/page.templ", loaders.About, options),
}
```

The cache key covers the route ID, scope, every path param, and every query value, so `[id]` routes never share entries. Only results that round-trip through the codec losslessly are stored. After a mutation — typically from a server action — drop the stale entry through the invalidator:

```go
demoInvalidator.Invalidate(ctx, route, "about-v1")
```

Concurrency safety comes in two layers: an invalidation fence (every cached binding over a store must attach the same invalidator; the registry rejects mismatches) and optional singleflight (`loader.FlightGroup`) that coalesces concurrent loads of the same key. For persistence across restarts, pass `cache.NewDiskStore(cache.DiskConfig{Dir: "var/cache"})` instead of memory — same contract. See the [caching guide](caching.md).
