# Releases and migration

Meta is in beta: public APIs and the starter layout can change between releases. Pin the CLI and Go module versions your project uses, and read the notes for each release before upgrading. This guide explains the release cadence, how to upgrade a project, and the migration steps for each released version.

## Versioning

- Pre-1.0 semantic versioning with pre-release labels: `v0.1.0-alpha.N` during early development, `v0.1.0-beta.N` while APIs settle. Beta releases can still break starter layout and copied runtime files.
- The Go module, the `meta` CLI, and the Clerk integration are versioned independently. A project pins each one explicitly:

```sh
go get github.com/akashKarmakar02/meta@v0.1.0-beta.6
go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.6
```

The module path is `github.com/akashKarmakar02/meta`. There is no `go-meta` module; older material referencing that name predates the rename.

- GitHub releases carry the notes for every tag: <https://github.com/akashKarmakar02/meta/releases>

## How to upgrade a project

```sh
# In your project directory:
go get github.com/akashKarmakar02/meta@v0.1.0-beta.6
go mod tidy
go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.6
meta generate   # regenerate routes for the new runtime
go build ./...  # or `meta build`
npm install     # if the starter's package.json changed
npm run check   # if your project has the check script
```

Two things a plain `go get` does **not** update:

1. **The copied browser runtime** (`frontend/*.ts`). Your project owns those files since `meta init`. When a release changes them, merge the changes by hand — compare against `frontend/` in the framework repository at the release tag.
2. **Starter configuration files** (`tsconfig.json`, `vite.config.ts`, `package.json`, scaffold comments). Apply the migration steps below when you want the new behavior.

After upgrading, run your full checks (`go test ./...`, `npm run check`, `npm run build`) and click through your app in development before deploying.

## Migration guide

### To v0.1.0-beta.6

Fixes beta.5 full-response cache poisoning between HTML and navigation JSON.
One eager generation now runs the loader, page and visual layout once, then
stores the complete HTML document and existing navigation envelope atomically.
Request order no longer affects correctness; cache hits skip loaders/rendering.
Mixed concurrent misses share one generation. Navigation still returns
`application/json` with `Cache-Control: no-store`.

#### 1. Upgrade the framework and CLI together

Run these commands from your application directory:

```sh
go get github.com/akashKarmakar02/meta@v0.1.0-beta.6
go mod tidy
go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.6
meta --version
```

A released CLI should report `v0.1.0-beta.6`. If your `go.mod` still replaces
`github.com/akashKarmakar02/meta` with a local framework checkout, remove that
replacement when switching to the published version. The Clerk integration is
versioned independently and does not need a new version for this release.

#### 2. Review your production page-cache bootstrap

**Caching behavior changes:** `NewPageCache(store, ttl)` remains source
compatible but caches nothing until you declare reviewed public route IDs.
`go get` does not rewrite your application's `cmd/app/main.go`.

You can keep caching disabled while upgrading. To enable it for reviewed pages,
replace beta.5's automatic cache-all configuration with an explicit public list:

```go
// Construct the store only in production. Use your existing configured store.
publicPages := []string{"about/page.templ", "blog/[slug]/page.templ"}
cfg.PageCache = pagecache.NewPageCache(store, cache.Never,
    pagecache.PublicRoutes(publicPages...),
    pagecache.BuildID("myapp-release-2026-11-10"),
)
```

Use generated source filenames as IDs, not URL patterns. Opt in only anonymous,
deterministic pages whose page, selected layout and loader are independent of
user/session/tenant identity, private headers, CSRF tokens and CSP nonces.
Never opt in personalized or protected pages. Keep authorization and tenant
checks before dispatch; middleware handling private request context must reject
the request or mark it `private, no-store` before `c.Next()`.

New scaffolds start with an empty `publicPages` list and construct no page cache
until it is populated. They read `GO_META_BUILD_ID` for the application's release
identity. Change it whenever templates, loaders, layout selection or assets
change. It should identify your whole app release, rather than only the Meta
version. When omitted, Meta fingerprints the executable; assets deployed
independently require an explicit ID.

Cookie/authorization requests, Set-Cookie, private/no-store, nonce and Vary
responses bypass shared caching. Existing policy and CSRF middleware still run
on every request. Global CSRF no-store behavior can make all pages ineligible;
keep that protection. Development (`DevOrigin`) disables response caching.
`cache.Never` requires mutation invalidation; a finite TTL remains available.

Applications using `server.Config.PageCache` get the eager pair generator
automatically. A custom `Wrap(next)` integration now passes through unless it
supplies a deliberate pair generator; it cannot populate a pair by capturing
one HTTP response. Prefer the public server integration. See the
[caching guide](caching.md) for the custom resolver contract.

#### 3. Preserve disk data and wire invalidation

No manual `.pagecache/` deletion is needed for correctness. The new
`ssr-page:v3` namespace ignores beta.5's ambiguous entries and regenerates both
representations on the first eligible HTML or navigation request.

`InvalidatePath(ctx, "/blog/example")` invalidates HTML, JSON and all query
variants together, including after a disk-backed restart. In-flight older
renders cannot republish into the current generation. `Invalidate(ctx, route)`
now also invalidates every query variant for the concrete path. Invalidate after
committing application mutations and check returned errors. If your loader is
independently cached with `BindCached`, invalidate its data before the page;
sitemap invalidation remains separate.

Use one PageCache coordinator per store and one active process per disk cache
directory. Sequential restarts are supported; multi-process invalidation is not
provided. Persist the cache directory as a container volume if restart reuse
is desired. Old unreachable entries with `cache.Never` need occasional offline
reclamation to bound disk growth; do not delete an active cache's token metadata
as an invalidation mechanism.

#### 4. Regenerate, verify, and deploy

```sh
meta generate
go test ./...
npm run check       # when provided by your app
meta build          # Go binary plus Vite assets
```

No frontend runtime copy, Node dependency change, navigation protocol update,
or `cache.Store` adapter migration is needed for beta.5 → beta.6. Svelte remains
CSR-only and Node remains build-time-only. Earlier versions may still require
the frontend updates in their migration sections below.

Before deploying, check your app's anonymous and signed-in flows:

- Request a public templ route directly, then navigate to it softly; repeat in
  the opposite order. Both must preserve the correct content type and metadata.
- Exercise a Svelte-to-templ link, templ-to-templ link and browser back/forward.
- Confirm repeated eligible visits skip loader/render work; cookie-bearing
  sessions should bypass the shared cache.
- Change content and invalidate; verify both representations update, including
  after restarting with the same disk directory.
- Confirm required authorization still rejects denied requests and assets load.

The framework's release validation passed `make verify` and focused cache/server/
renderer race tests: 248 Vitest passes (one existing skip), 132 Chromium passes
(12 existing native-image skips), and seven CORS passes. See the
[implementation report](../dual-representation-implementation.md) for the full
file list, measurements, and deployment limits.

### To v0.1.0-beta.5

The caching release. New packages: `cache.NewDiskStore` (persistent disk store), `sitemap` (disk-backed sitemap XML), `pagecache` (SSR page response caching), and `metadata.SitemapCache`. New guide: [Caching](caching.md).

**For new projects** (`meta init` from this release): production builds enable disk-backed SSR page caching by default — indefinite TTL, stored under `.pagecache/` (gitignored). In development (`GO_META_DEV=1`) no page cache is constructed at all.

**Existing projects** can adopt each piece:

```sh
go get github.com/akashKarmakar02/meta@v0.1.0-beta.5
go mod tidy
```

1. **Default prod page cache** — replace the app bootstrap's config (see `cmd/app/main.go` in a fresh scaffold at this tag):

```go
// Constructed only when NOT in development; see comments in main.go.
store, err := cache.NewDiskStore(cache.DiskConfig{Dir: ".pagecache"})
if err != nil { log.Fatal(err) }
cfg.PageCache = pagecache.NewPageCache(store, cache.Never)
```

2. **Indefinite TTL** — `cache.Policy{TTL: cache.Never}` on any `Set` stores an entry that never expires (both stores; safe across disk restarts). Expiry-bounded policies are still capped at 24h.

3. **Path invalidation after mutations**:

```go
pc.InvalidatePath(ctx, "/blog/hello-world") // from action handlers
```

4. **Sitemap TTL declared per page** (in any `page.templ`):

```go
var SitemapCache = metadata.SitemapCache{
    TTL: 24 * time.Hour, // omit for indefinite
}
```

**Breaking change (beta-only surface):** `server.Config.PageCache` moved from the internal `httpserver` package to the public [`pagecache`](https://pkg.go.dev/github.com/akashKarmakar02/meta/pagecache) package — internal packages were unimportable from user code, so the old field was unusable in practice. `NewPageCache(store, ttl)` takes the TTL directly.

### To v0.1.0-beta.4

New in this release (no breaking changes):

- **CORS support.** First-class, opt-in cross-origin resource sharing through Fiber v3's `cors` middleware registered in `server.Config.Middleware`. Meta enables nothing by default; actions, forms, and route policies keep their own protections. New guide: [CORS](cors.md).
- **`@/` import alias in new projects.** Projects created by `meta init` with this release map `@/` to the project root in TypeScript and Svelte files, like Next.js.

Existing projects that want the `@/` alias apply three small edits (new projects get them automatically):

```jsonc
// tsconfig.json — add to compilerOptions
{
  "moduleResolution": "bundler",
  "paths": {"@/*": ["./*"]}
}
```

```ts
// vite.config.ts — inside defineConfig
resolve: {
  alias: [{ find: '@', replacement: new URL('.', import.meta.url).pathname }],
},
```

```jsonc
// package.json — devDependencies for type checking
"@types/node": "^26.6.6",
"svelte-check": "^4.7.7"
```

Then add a `"check": "svelte-check --tsconfig ./tsconfig.json"` script if you want one. Restart your editor after changing `tsconfig.json`.

### To v0.1.0-beta.3

- Adds a starter `tsconfig.json` with `vite/client` types, so TypeScript recognizes `import.meta.env` in newly initialized projects.

Existing projects can add `"types": ["vite/client"]` under `compilerOptions` in their own `tsconfig.json`.

### To v0.1.0-beta.2

- Adds `LayoutOutlet` so Svelte pages receive context from providers in their layout while preserving existing plain outlets.
- Expands routing and Clerk documentation.

Projects created before this release must merge the updated frontend runtime files into their project before using `LayoutOutlet`; updating the CLI or Go module alone does not replace copied runtime files.

### To v0.1.0-beta.1

First beta release.

- Experimental Clerk integration published separately as `github.com/akashKarmakar02/meta/integrations/clerk`. It has not been verified against a live Clerk instance and does not yet implement Clerk's full session handshake or refresh flow; see the integration documentation before use.

### Earlier releases

Alpha releases (up to `v0.1.0-alpha.13`) tracked phase gates rather than user-facing changes. If you are upgrading across many alpha releases, regenerate your project's frontend runtime from the target tag's `frontend/` directory and re-run `meta generate`.
