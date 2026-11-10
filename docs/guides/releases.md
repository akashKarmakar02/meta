# Releases and migration

Meta is in beta: public APIs and the starter layout can change between releases. Pin the CLI and Go module versions your project uses, and read the notes for each release before upgrading. This guide explains the release cadence, how to upgrade a project, and the migration steps for each released version.

## Versioning

- Pre-1.0 semantic versioning with pre-release labels: `v0.1.0-alpha.N` during early development, `v0.1.0-beta.N` while APIs settle. Beta releases can still break starter layout and copied runtime files.
- The Go module, the `meta` CLI, and the Clerk integration are versioned independently. A project pins each one explicitly:

```sh
go get github.com/akashKarmakar02/meta@v0.1.0-beta.5
go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.5
```

The module path is `github.com/akashKarmakar02/meta`. There is no `go-meta` module; older material referencing that name predates the rename.

- GitHub releases carry the notes for every tag: <https://github.com/akashKarmakar02/meta/releases>

## How to upgrade a project

```sh
# In your project directory:
go get github.com/akashKarmakar02/meta@v0.1.0-beta.5
go mod tidy
go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.5
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

### To v0.1.0-beta.6 (prepared; not yet published)

Fixes beta.5 full-response cache poisoning between HTML and navigation JSON.
One eager generation now runs the loader, page and visual layout once, then
stores the complete HTML document and existing navigation envelope atomically.
Request order no longer affects correctness; cache hits skip loaders/rendering.
Mixed concurrent misses share one generation. Navigation still returns
`application/json` with `Cache-Control: no-store`.

**Security migration:** shared SSR caching now requires explicit public route
IDs. Existing `NewPageCache(store, ttl)` calls still compile but cache nothing
until reviewed routes are added:

```go
cfg.PageCache = pagecache.NewPageCache(store, cache.Never,
    pagecache.PublicRoutes("about/page.templ", "blog/[slug]/page.templ"),
    pagecache.BuildID("your-entire-release-id"),
)
```

New scaffolds leave the public list empty. Remove beta.5's automatic cache-all
bootstrap or replace it with this policy. Never opt in personalized, protected,
tenant-sensitive, CSRF-token or nonce-bearing pages. Cookie/auth requests and
unsafe responses bypass caching; middleware and authorization still execute.
Development (`DevOrigin`) disables it. Custom `Wrap(next)` integrations must
provide a deliberate pair generator; without one they now pass through safely.
`server.Config.PageCache` integrations are wired automatically.

`ssr-page:v3` ignores old beta.5 entries; no manual `.pagecache/` deletion is
needed for correctness. A release ID prevents reuse across changed templates,
loaders and assets. The default fingerprints the executable; independently
deployed assets require an explicit ID. The scaffold reads `GO_META_BUILD_ID`.

`InvalidatePath` now replaces a durable path-generation token: HTML, JSON and
all query variants invalidate together after restart. In-flight older renders
cannot republish into the new generation. `Invalidate(ctx, route)` now also
invalidates all concrete-path query variants. Use one coordinator/active process
per store/disk directory; multi-process invalidation is not provided. Old
unreachable `cache.Never` entries require offline reclamation to bound disk use.
Also invalidate separately cached loader data before regenerating a page.

No navigation protocol, frontend runtime copy, public `cache.Store` contract,
SSR/CSR boundary, or production runtime change is required. See the
[caching guide](caching.md) and [implementation report](../dual-representation-implementation.md)
for validation, benchmarks and operational limits.

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
