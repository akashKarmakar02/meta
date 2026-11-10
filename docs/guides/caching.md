# Caching

Meta's caching is one contract with three stores and two layers. The same
`cache.Store` interface backs loader data caching, the sitemap, and full SSR
HTML response caching; the same `cache.Policy` governs TTL; the same
invalidation fence guarantees a mutation never serves stale data.

```text
request ──► PageCache.Wrap ──► WithRouteParams ──► your page
                │ hit                          │ miss (render)
                ▼                              ▼
            store.Get                    loader cache (BindCached)
                                              │ miss
                                              ▼
                                         your loader function
```

## The contract

Every store implements `cache.Store`:

```go
type Store interface {
    Get(ctx context.Context, key Key) (Entry, bool, error)
    Set(ctx context.Context, key Key, entry Entry, policy Policy) error
    Delete(ctx context.Context, key Key) error
    Close(ctx context.Context) error
}
```

Entries hold opaque bytes. `Key` is an opaque string built with
`cache.NewKey(parts ...string)` — components are length-prefixed so `("a","bc")`
and `("ab","c")` never collide. Policies carry `TTL`, `StaleTTL`, and `Tags`;
both TTLs must be positive and TTL ≤ 24h (`cache.MaxTTL`) — a cache in this
framework is a bounded-TTL structure by design, not a permanent database.

## Loader data caching

The layer most apps touch. Wrap a typed Go loader with the generated binding:

```go
// app/loaders/posts.go — hand-written, uses the loader package directly.
binding := loader.BindCached("posts-index", loadPosts, loader.CacheOptions[[]Post]{
    Store:  store,
    Policy: cache.Policy{TTL: time.Minute, Tags: []string{"posts"}},
    Scope:  myScopeFn,        // func(ctx, route) (scope string, cacheable bool, err error)
    Invalidator: invalidator, // same store as Store; wired into the fence
})
```

`Scope` decides what the key includes beyond route params (e.g. the current
user) and whether a request is cacheable at all — return `cacheable: false`
for anything user-specific or uncacheable. Pass `Flights` (built with
`loader.NewFlightGroup(invalidator, timeout)`) so concurrent requests for the
same key run the load once; the sitemap and page cache already singleflight
internally.

## Stores

**Memory** — the default, fastest, volatile. Bound it explicitly;
there are no implicit defaults.

```go
store, err := cache.NewMemory(cache.MemoryConfig{
    MaxEntries:     1024,
    MaxBytes:       64 << 20, // accounted: len(key)+len(value)+entryOverhead per entry
    MaxEntryBytes:  1 << 20,  // zero disables the per-entry limit
})
```

**Disk** — persistence across restarts for loader data, sitemaps, and SSR HTML.
Atomic writes (temp file + rename), CRC-checked envelopes, lazy expiry, optional
background sweeper. Same contract; a corrupt file surfaces as a miss, never a
crash.

```go
store, err := cache.NewDiskStore(cache.DiskConfig{
    Dir:           "/var/lib/myapp/cache", // absolute in production
    MaxEntryBytes: 4 << 20,
    SweepInterval: time.Minute,
})
```

Both stores reject invalid configuration at construction with an explicit
error (`ErrInvalidConfig`) — a typo cannot silently produce an unbounded cache.

## Sitemap caching

`sitemap.New(sitemap.Config{...})` builds a URL set from static routes (routes
with `[param]` segments are excluded because their keys are unknown at build
time). Pass the same store you use elsewhere and it persists across restarts.
Invalidate explicitly when content changes:

```go
sm, err := sitemap.New(sitemap.Config{
    BaseURL: "https://example.com",
    Source:  sitemap.RouteSource{Routes: myRoutes},
    Store:   diskStore,
})
xml, err := sm.XML(ctx)
```

See the [routing guide](routing.md) for how route metadata feeds `lastmod` and
priority.

## SSR HTML response caching

Full-page caching of templ-rendered HTML. Opt-in per app via
`server.Config.PageCache`; CSR/Svelte pages are never cached by it (their HTML
shell is static, hydration data flows through loader caching instead).

```go
app, err := server.New(server.Config{
    // ...
    PageCache: httpserver.NewPageCache(httpserver.PageCacheConfig{
        Store: diskStore,
        TTL:   30 * time.Second,
    }),
})
```

Only successful, non-navigation-envelope responses are stored. On a hit, the
middleware serves the cached bytes before any rendering runs. Keys include
route pattern + params + query string, so `/blog?page=2` never collides with
`/blog?page=3`.

## Invalidation

Three layers coordinate through the shared invalidator:

1. **Loaders**: `invalidator.Invalidate(ctx, route, "posts")` drops loader
   entries tagged with the `"posts"` scope.
2. **Pages**: the same invalidation bumps a generation counter; the next
   request re-renders and re-stores fresh HTML.
3. **Sitemap**: `sm.Invalidate(ctx)` regenerates on next request. Wire it into
   the same action that mutates content.

The fence: if a cached binding and its invalidator disagree about store
identity, the registry rejects the wiring at startup. You cannot build a
"stale-forever" cache by accident.

## What not to do

- Don't cache sessions or per-user data in the page cache — keys don't include
  cookies. Loader caching with user-specific key parts is the right tool.
- Don't set TTL above what your data tolerates. Cache expiry bounds staleness;
  invalidation removes it early when you know better.
- Don't share one store between apps with untrusted tenants — keys are namespaced,
  not access-controlled.
