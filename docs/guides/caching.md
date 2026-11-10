# Caching

Meta uses the same `cache.Store` contract for typed loader data, sitemap XML,
and complete SSR response pairs. Each layer is explicitly configured.

```text
request → required middleware → route context → authorization / CSRF
        → public-page eligibility → page cache lookup
          ├─ hit: select cached HTML or navigation JSON (no loader/render)
          └─ miss: loader once → page + layout once → HTML and JSON
                   → fence check → atomically persist pair → select response
```

## The contract

Every store implements `cache.Store`:

```go
type Store interface {
    Get(ctx context.Context, key Key) (Entry, bool, error)
    Set(ctx context.Context, key Key, entry Entry, policy Policy) error
    Delete(ctx context.Context, key Key) error
}
```

Entries hold opaque bytes. `Key` is an opaque string built with
`cache.NewKey(namespace string, components ...string)` — components are length-prefixed so `("a","bc")`
and `("ab","c")` never collide. Policies carry `TTL`: a positive duration up to 24h (`cache.MaxTTL`), or
`cache.Never` for content governed by explicit invalidation. Concrete memory
and disk stores also expose `Close() error`; it is not part of `Store`.

## Loader data caching

The layer most apps touch. Wrap a typed Go loader with the generated binding:

```go
// app/loaders/posts.go — hand-written, uses the loader package directly.
binding := loader.BindCached("posts/page.templ", loadPosts, loader.CacheOptions[[]Post]{
    Store:  store,
    Policy: cache.Policy{TTL: time.Minute},
    Codec:  loader.JSONCodec[[]Post](),
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

**Disk** — persistence across restarts for loader data, sitemaps, and SSR pairs.
Atomic writes (temp file + rename), CRC-checked envelopes, lazy expiry, optional
background sweeper. Same contract; a corrupt file surfaces as a miss, never a
crash.

Both stores accept `TTL: cache.Never` — an entry that never expires on its own
and lives until invalidated or deleted. Use it only for explicitly safe public pages with mutation invalidation.
New scaffolds leave page caching disabled until public route IDs are declared.

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
    RouteSource: sitemap.Source{{Path: "/"}, {Path: "/about"}},
    Store:   diskStore,
})
xml, err := sm.XML(ctx)
```

See the [routing guide](routing.md) for how route metadata feeds `lastmod` and
priority.

The sitemap's own persistence TTL is declarable in any `page.templ` file:

```templ
package app

// SitemapCache controls how long the generated sitemap XML is persisted.
// cache.Never (the default) keeps it until sm.Invalidate is called.
var SitemapCache = metadata.SitemapCache{
    TTL: 24 * time.Hour,
}
```

templ passes package-level declarations straight through to the generated Go
file, so this lands in your editor's intellisense like any other declaration.
Omit it and the sitemap persists indefinitely, regenerating only on explicit
invalidation.

## Eager dual-representation SSR caching (beta.6)

A cache entry represents one logical generation of a public templ SSR page:
a complete HTML document **and** Meta's version-1 navigation JSON envelope.
The first eligible GET generates and stores both before responding, regardless
of whether HTML or navigation arrived first. Later requests select their
pre-generated representation without running a loader, page, or layout.
Svelte stays client-only; CSR shells and data, actions, APIs, and assets are
outside this cache. Production remains a Go binary plus built assets.

Caching requires both `server.Config.PageCache` and explicit source-derived
route IDs in `pagecache.PublicRoutes`. Constructing a cache with no route IDs
stores nothing. `meta init` now scaffolds an empty public-page list instead of
automatically caching every SSR page.

```go
pc := pagecache.NewPageCache(diskStore, cache.Never,
    pagecache.PublicRoutes("blog/[slug]/page.templ", "about/page.templ"),
    pagecache.BuildID("myapp-release-2026-11-10"),
    pagecache.GenerationTimeout(15*time.Second),
)
app, err := server.New(server.Config{
    // Generated Routes, SSRPage, BarePage and VisualLayout, plus Loaders...
    PageCache: pc,
})
```

Use a finite duration such as `30*time.Second` for bounded freshness.
`cache.Never` requires explicit invalidation after every relevant mutation.
`BuildID` should identify the entire application release, including assets.
When omitted, Meta hashes the running executable once; if it cannot read the
executable, it uses a process-specific ID and safely forgoes restart reuse.
If assets are deployed independently of the binary, set an explicit release ID.
The scaffold accepts `GO_META_BUILD_ID` (change it for every release).

The renderer resolves the bare templ page, then wraps a capture component in
the selected visual layout. At the layout's child invocation it renders the
page into a buffer once, with layout-provided context and typed loader data,
then writes those same bytes into the layout. A repeated child invocation
replays bytes. The layout also renders once. Page metadata overrides layout
metadata even when the layout sets defaults after its child. The document
builder and existing navigation-envelope builder use the same visual HTML and
structured metadata, including canonical, Open Graph, and Twitter fields.
No final-document parsing, loopback request, fabricated Fiber context, second
loader, or second templ tree is involved. Only document assembly and JSON
serialization are representation-specific.

Custom resolvers must follow the generated-resolver contract: `SSRPage` is
`BarePage` composed through `VisualLayout`, and the visual layout renders the
child. A layout that never invokes its child fails pair generation; it cannot
publish a partial pair. Cache-enabled pages must be deterministic and free of
per-request side effects.

### Eligibility and HTTP behavior

Opt in only pages whose **entire** output is public, anonymous, deterministic,
and independent of user/session/tenant identity, private headers, CSRF tokens,
and CSP nonces. A public URL is not proof of public data. Applications must
keep personalized routes out of `PublicRoutes`. Middleware handling custom
identity/tenant headers must reject the request or set `private, no-store`
**before** calling `c.Next()`; setting policy after dispatch cannot protect a
lookup. Tenant-scoped shared page caching is not provided.

Before lookup, Meta rejects cookie-bearing requests, Authorization and
Proxy-Authorization, private/no-store controls, Set-Cookie responses, CSP
nonces, non-200 status, and Vary responses. The same checks run after generation
before storing or sharing output. Required application middleware, route
policies, and CSRF middleware still run on every hit. Protected policies mark
responses `private, no-store`; CSRF middleware uses no-store and may issue a
cookie. Neither is bypassed by caching. Requests with those policies retain
the ordinary uncached path. Redirects, errors, and negative results are not
cached.

HTML returns `text/html; charset=utf-8`. Navigation uses the existing
`X-Go-Web-Navigation: 1` header, exact version-1 envelope, `application/json`,
and `Cache-Control: no-store`. The protocol no-store is applied after internal
cache decisions: it forbids browser/proxy storage while the explicitly
controlled application cache can retain the pair. An application's own
no-store prevents lookup and storage.

Only status, content type, Cache-Control, Content-Language, and body are stored.
Each response owns its body buffer, protecting coalesced output from downstream
middleware mutation.
Cookies, request IDs, traces, session headers, and nonces are never replayed.
Request-specific/security headers belong in middleware that runs on every
request. Unsupported navigation versions receive HTML as before.

### Persistence, format, and upgrades

One opaque `cache.Entry` under namespace `ssr-page:v3` holds both responses.
It uses magic `META-SSR` plus version byte 3, bounded little-endian lengths and
status fields, header/body bytes for each representation, and a CRC32 over the
whole envelope. Hits use slices into the store's private copy. The cache store
contract remains unchanged. A single atomic store write publishes the pair
with one TTL; disk uses its existing atomic rename and outer CRC envelope.

Keys cover deployment ID, route identity/pattern, exact requested URL, and a
path-generation token. Exact URL spelling preserves the navigation `url`
field, including query order; query variants share the same invalidation token.
Beta.5's ambiguous `page` keys and NUL-separated responses are never read.
Upgrading automatically causes a fresh generation; deleting `.pagecache/` is
not required for correctness. A corrupt or incomplete pair is a miss.

### Invalidation and concurrency

```go
if err := db.UpdatePost(ctx, slug); err != nil { return err }
return pc.InvalidatePath(ctx, "/blog/"+slug)
```

`InvalidatePath` replaces a persistent random path-generation token in the
same store under `ssr-path:v1`. This invalidates both representations and all
query variants, even after restart and even before the new process serves the
path. Escaped path spellings share a decoded-path token. No full disk scan or
process-local variant index is needed. `Invalidate(ctx, route)` also advances
the concrete path's token using declaration pattern and params; it invalidates
all query variants. Check returned errors: a failed token write does not
promise invalidation.

A fence orders the token check and page write against invalidation. An old
in-flight request may return its original snapshot, but cannot repopulate the
current cache generation after successful invalidation. A request beginning
after invalidation uses the new token and cannot join the old flight. Concurrent
HTML and JSON misses for one identity share a single flight. Failure stores
nothing and releases waiters; the next request can recover.

The owning Fiber handler generates synchronously, with a 30-second default
context bound; loaders/components must honor cancellation. Waiters cancel
independently. Owner cancellation prevents storage and lets remaining waiters
retry. There are no background generation goroutines or retained Fiber objects;
server shutdown retains its normal request-lifetime semantics. Store failures
are observable and fail open to rendering. A failed write cannot publish a
half-pair or fail an otherwise successful page response.

Use **one PageCache coordinator per store and one active process per disk
cache directory**. Sequential restarts are supported. Distributed/multi-process
invalidation and crash/power-loss durability beyond the existing disk store
are not promised. Invalidate each instance when using separate stores. Do not
share one directory between independently running coordinators.

Token replacement leaves older entries unreachable. Finite TTL expires those
page entries normally; `cache.Never` entries and per-path token metadata need
periodic offline directory reclamation during deployment if disk growth matters.
An evicted/missing token gets fresh randomness, so it cannot resurrect old data.
Regeneration may still observe cached loader data: if using `BindCached`, also
invalidate its loader cache after the mutation before invalidating the page.
Sitemap invalidation remains separate (`sm.Invalidate(ctx)`).

### Development and troubleshooting

New scaffolds construct no page cache in development. `server.Config.DevOrigin`
also disables response caching even if a PageCache is supplied. Live edits keep
the existing single-representation render path and do not incur eager pair
construction.

If navigation returns HTML, inspect `X-Go-Web-Navigation: 1`, content type,
and the client pipeline log (`?go-meta-debug=1`, `window.__goMetaNavLog`). Check
that a public route ID matches its generated source filename, that no cookies,
authorization, CSRF, private/no-store, nonce, or Vary policy makes it ineligible,
and that the application is not in development. An unsupported header version
intentionally falls back to HTML. Confirm the deployment ID when investigating
stale assets. Never log cached bodies or sensitive keys.

Existing observability emits `pagecache` events: `dual` hit/miss; `generation`
started/completed/failed/coalesced/stale-rejected; `write` failed; `lookup`
error; and `cache` invalidated/error. Labels contain route IDs and fixed
outcomes, never URLs, keys, or bodies. Invalidation events use the caller's
observability context when available.

The repository includes mixed-request integration/race tests and Chromium
navigation tests. Run `make verify`; benchmark the production HTTP paths with:

```sh
go test ./server -run '^$' -bench BenchmarkSSRRepresentations -benchmem -count=5
```

See the [implementation report](../dual-representation-implementation.md) for
validation results, benchmark samples and deployment limitations.
