# Eager dual-representation SSR cache — beta.6 implementation

## Root cause and baseline

Inspected latest remote main at `c88054ae03508a83b8c26fe6370d4f1296f88ad5`.
Beta.5 used the same cache identity for HTML and navigation, storing whichever
content type/body arrived first. A later request for the opposite type received
the wrong bytes. Loaders ran outside the response-cache wrapper, even on hits.
Path invalidation relied on a process-local index lost across disk restarts.
The scaffold also enabled indefinite shared caching for every SSR route.

The [pre-change investigation](dual-representation-investigation.md) records the
original pipeline before modification. The referenced phase-1 blueprint is
absent from the checkout and Git tree; the supplied working guide and task
architecture constraints were followed.

## Rendering and middleware architecture

Before: application middleware → route context → policy/CSRF → loader → cache
→ single-representation renderer. Normal SSR resolved a composed page; soft
navigation rendered a bare page and then the selected visual layout. Both
collected metadata, but through separate paths.

Now: application middleware → route context → policy/CSRF → public eligibility
→ cache lookup → on a miss, one loader dispatch and one explicit pair generator.
Hits select HTML or JSON without loader/page/layout execution. The ordinary
uncached and development response paths remain available.

`SSRPairGenerator` resolves the bare page and selected visual layout, wrapping
the child in a capture component. It captures page bytes exactly when the
layout renders its child, preserving layout-provided context. A repeated child
invocation replays the captured bytes. One shared metadata collector marks
page metadata for precedence over layout defaults. The framework then builds
both a complete document (including assets) and the existing version-1
navigation envelope from the same visual HTML and metadata snapshot.

Page execution: once. Layout execution: once. Loader execution: once per
successful cache generation, unless separately cached loader data avoids an
underlying load. The additional work is document assembly and navigation JSON
serialization; no templ tree, database loader, HTTP request, or Fiber context
is duplicated. The navigation protocol remains `X-Go-Web-Navigation: 1`,
`application/json`, with `Cache-Control: no-store`.

Custom resolvers must satisfy the generated contract: composed SSR equals
BarePage wrapped through VisualLayout, and the layout invokes its child.
Missing child invocation fails generation rather than publishing a partial pair.

## Storage, versioning, and compatibility

One `cache.Entry` holds both representations. Namespace: `ssr-page:v3`.
Format: `META-SSR` plus version byte 3; for each representation, status (u16),
content-type length (u16), Cache-Control length (u16), Content-Language length
(u16), body length (u32), followed by those bytes; CRC32 covers the whole
preceding envelope. Lengths are little endian and validated before slicing.
Both response bodies reference the store's private returned copy on a hit.
A single Store.Set publishes the pair with one TTL, using existing disk atomic
rename and outer CRC protection. The public `cache.Store` contract is unchanged.

Identity covers application build, route identity/pattern, exact original URL,
and durable path token. Exact query spelling/order is preserved in envelope URLs.
Default build identity fingerprints the executable once. An explicit BuildID
should cover independently deployed assets too. Empty BuildID retains the safe
fingerprint default. If executable reading is unavailable, a random process ID
safely sacrifices persistence reuse.

Beta.5 `page` keys and content-type/NUL envelopes cannot be read under the new
namespace. Upgrades regenerate automatically, without deleting `.pagecache/`.
Malformed, corrupt, unsupported and incomplete envelopes are misses. Existing
`server.Config.PageCache` wiring remains source compatible. Public route IDs
are now mandatory; legacy Wrap(next) calls without a deliberate pair generator
pass through rather than capturing an ambiguous single response.

## Invalidation and concurrency guarantees

InvalidatePath replaces a random per-path generation token persisted in the
same store under `ssr-path:v1` with TTL Never. Both representations and every
query/escaped spelling variant become unreachable together, without a disk
scan. A fresh process can invalidate before serving the path. Tokens themselves
survive subsequent restarts. Missing/evicted tokens get fresh randomness,
preventing resurrection of old entries. Invalidate(ctx, route) derives the
concrete path from declaration pattern/params and invalidates all its variants.

A coordinator lock orders token checks and page writes against invalidation.
A generation started before invalidation may finish its original request, but
cannot populate the current token after invalidation succeeds. New requests
cannot join the obsolete flight. HTML/JSON misses share a single flight by
pair identity. Errors store no entry, release waiters, and allow recovery.

Generation runs synchronously on its owning request, bounded by a 30-second
context by default. Cancellation is cooperative. Waiters cancel independently;
owner cancellation prevents storage and lets remaining requests retry. No Fiber
object is retained past handler lifetime. Header strings retained by a flight
are copied out of Fiber's borrowed buffers. Each response gets its own body
buffer so downstream middleware cannot mutate bytes shared by other waiters.
No background generation goroutines
or extra shutdown lifecycle are introduced. Cache write failures are observed
and do not fail an otherwise successful response.

## Security and policy changes

Only source IDs explicitly listed in PublicRoutes are candidates. New scaffold
projects start with an empty list, and development never enables shared response
reuse. Public pages must be anonymous, deterministic and independent of tenant,
identity, private headers, CSRF tokens and nonces. Middleware for application
identity/tenant headers must reject or mark private/no-store before dispatch.

Cookie, Authorization and Proxy-Authorization requests bypass lookup and writes.
Non-200 outcomes, Set-Cookie, private/no-store controls, CSP/report-only nonces,
and Vary responses also prevent caching. Multiple Cache-Control/CSP header lines
are inspected. Policies and CSRF middleware precede every cache hit. Errors and
redirects are never negatively cached. Protocol navigation no-store is applied
after internal caching, preserving its external HTTP meaning.

Only status, Content-Type, Cache-Control, Content-Language and body persist.
Request IDs, traces, session headers, cookies and nonces are not replayed.
Request-specific headers are regenerated by middleware on every request.

## Changed files

| Area | Files |
| --- | --- |
| Shared output/rendering | `internal/ssrresponse/page.go`, `internal/render/dual.go`, `internal/render/dual_test.go` |
| Cache/coordination/format | `pagecache/pagecache.go`, `pagecache/build.go`, `pagecache/envelope.go`, `pagecache/pagecache_test.go`, `pagecache/envelope_test.go` |
| Server and example | `server/server.go`, `cmd/example/main.go`, `internal/cli/scaffold/cmd/app/main.go.tmpl` |
| Integration/benchmarks | `server/pagecache_integration_test.go`, `server/dual_pagecache_test.go`, `server/dual_pagecache_bench_test.go` |
| Browser verification | `tests/fixtures/pagecache/main.go`, `tests/browser/dual-pagecache.spec.ts`, `playwright.config.ts`, `Makefile` |
| Measurements | `docs/benchmarks/eager-dual-ssr-cache.txt` |
| Documentation | `docs/guides/caching.md`, `docs/guides/routing.md`, `docs/guides/releases.md`, investigation and this report |

No generated files were edited, no dependencies added, and no frontend runtime
or Svelte SSR change was introduced. Beta.6 includes the [upgrade guide](guides/releases.md#to-v010-beta6) and
release notes for application migration.

## Validation and measurements

The final `make verify` passed on the implementation, including:

- templ/route generation, Go vet and every root-module Go test;
- scaffolded application compilation and real Air route-reconciliation tests;
- Svelte check: 0 errors, 0 warnings; TypeScript: passed;
- Vitest: 24 files passed, 248 tests passed, 1 existing skip;
- production Go binaries and Vite asset build; gofmt and diff hygiene;
- Chromium: 132 passed, 12 existing native-image/backend skips;
- separate real-browser CORS suite: 7 passed.

Both new cache browser tests passed. They cover direct navigation, HTML-first
and navigation-first population, Svelte-to-templ and templ-to-templ trusted
clicks, back/forward, repeated cache hits, metadata/title changes, document
identity and asset loading. No new cache test was skipped.

The focused `go test -race ./pagecache ./server ./internal/render` also passed.
Regression coverage includes coherent metadata and loader counts, mixed-miss
singleflight, restart/persistent invalidation, slow stale-fill fencing, old disk
entries, malformed envelopes, missing tokens, failed render/layout/loader/write
recovery, redirect/error outcomes, cancellation, dev bypass, release isolation,
URL spelling/query variants, safe headers, credential/policy rejection, and
response-buffer ownership against mutating downstream middleware.

Environment adjustments for this workspace (no test exclusions):

```sh
GOCACHE=/tmp/meta-go-build CCACHE_DIR=/tmp/meta-ccache \
  npm_config_cache=/tmp/meta-npm-cache GO_META_VITE_PORT=32173 \
  GOFLAGS=-buildvcs=false make verify
```

Port 5173 was already occupied by a live development server; the existing CLI
supervisor test hangs after that prerequisite fails. A scratch port preserved
that server. Temporary scaffold builds could not obtain VCS status in this
execution environment, so VCS stamping was disabled. Writable temporary build
caches accommodated the workspace sandbox. No application tests were disabled.

Benchmark command (10 samples per scenario, 300ms each, after verification
finished; no simultaneous build/test jobs):

```sh
GOCACHE=/tmp/meta-go-build CCACHE_DIR=/tmp/meta-ccache GOFLAGS=-buildvcs=false \
  go test ./server -run '^$' -bench BenchmarkSSRRepresentations -benchmem \
  -count=10 -benchtime=300ms
```

Linux/amd64, Go 1.26.10, AMD Ryzen 5 7520U, GOMAXPROCS=8.
These include Fiber dispatch, request/response handling, typed in-memory loader,
page/layout rendering and metadata; they are not isolated renderer timings.
Generation timing excludes invalidation/setup. The concurrent row measures one
batch of eight requests, rather than one request. The fixture loader has no
simulated database delay.

| Scenario | Median µs/op | Range µs/op | Median B/op | Median allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Uncached HTML | 54.81 | 53.73–58.58 | 18,304 | 96 |
| Uncached navigation | 58.03 | 57.16–66.45 | 17,923 | 100 |
| Eager pair miss | 104.58 | 102.14–129.34 | 26,656 | 166 |
| Cached HTML hit | 55.23 | 54.92–56.50 | 18,869 | 106 |
| Cached navigation hit | 56.46 | 55.96–63.59 | 18,446 | 108 |
| 8 concurrent mixed requests, one generation | 273.08 | 268.93–318.07 | 156,531 | 926 |

The pair miss median was 90.8% above the uncached HTML median and
80.2% above navigation for this tiny fixture. Both hits avoid loader and
templ execution; HTTP/test transport dominates a page this small. These are
observed scenario medians and ranges, not a universal production speedup or a
statistical before/after claim. Real savings depend on application loader and
template cost. Disk I/O and a real database are not measured here.

[All raw benchmark samples](benchmarks/eager-dual-ssr-cache.txt).


## Operational limits

- One PageCache coordinator per store; one active process per disk directory.
  Sequential restarts work. Distributed/multi-process fencing is outside the
  Store consistency contract. The coordinator serializes store operations;
  loader/render work runs concurrently for unrelated identities.
- Old token generations become unreachable, rather than synchronously deleted.
  Finite TTL expires page entries. Never entries and per-path token metadata
  need occasional offline reclamation to bound disk growth. Power-loss/fsync
  guarantees remain those of the existing disk store.
- Applications must invalidate separately cached loader data before page
  invalidation, and sitemap data separately. A response cache cannot infer
  dependencies or open a database snapshot for application queries.
- PublicRoutes is an explicit application safety declaration. Arbitrary custom
  identity/tenant context cannot be introspected; such pages must not opt in.
  Custom policy must run before dispatch, not after c.Next().
- Components/loaders must honor cancellation; Go cannot preempt an uncooperative
  function without unsafe request-lifetime background work.
- Explicit release IDs are required when assets change independently of the
  executable. Development uses the original single-representation render paths.
