# go-meta

A Go-first full-stack web framework. Phase 1 scope: file routing, server
rendering (templ), client rendering (Svelte 5 islands), layout ownership, and
soft navigation.

The implementation plan lives in [`phase1-implementation-blueprint.md`](phase1-implementation-blueprint.md).

## Visual layouts

Each page uses the nearest ancestor `layout.templ` or `layout.svelte`,
including one in its own directory. More distant layouts are not composed.
The framework supplies the HTML document independently. A templ layout may
wrap a templ or Svelte page; a Svelte layout supports Svelte pages. A templ
page under a Svelte layout is rejected because Svelte is client-only. Two layout
files in the same directory are an error.

## Dynamic templ packages

Templ pages in dynamic directories use `templ Page()` and templ layouts there
use `templ Layout(templ.Component)`. Other `.templ` files in that directory
share the same package. Generation compiles these files in a Go-safe generated
package, while route IDs retain their source paths. Dynamic directories cannot
contain `.go` files; put Go helpers in a normal Go package and import them.
Package-local `//go:embed` assets in dynamic templ packages are not yet staged.
Run `make generate` rather than invoking `templ generate` directly, so generated
Go is kept out of bracket-named source directories.

## Request route context

In templ rendering, `go-meta/routecontext` exposes the matched source-derived
route ID, declared pattern, B04 typed parameters, and query values:

```templ
import "go-meta/routecontext"

templ Page() {
    {{ route, _ := routecontext.FromContext(ctx) }}
    <p>{ route.RouteID }</p>
    <p>{ route.Pattern }</p>
    <p>{ route.Query.Get("tab") }</p>
    for _, tag := range route.Query.Values("tag") {
        <span>{ tag }</span>
    }
}
```

Use `route.Params.Int64("userId")` for a typed path parameter. The existing
`routeparams.FromContext(ctx)` returns the same parameters. Query `Get`,
`Values`, `Has`, and `All` preserve repeated keys; returned slices and maps
are copies, and query key case is preserved. Route snapshots can be retained
after the request. No context is
attached before route dispatch or for unknown, invalid, or unsupported-method
requests. Upstream Fiber middleware returning from `Next` can observe the
matched snapshot on `c.Context()`, but that context is already cancelled.
Route metadata is server-side and is not added to navigation JSON.

Matched-page queries use Go's `url.ParseQuery`: `+` and `%20` mean space, a key
without `=` has an empty value, and malformed percent escapes or unescaped
semicolons produce HTTP 400. Query strings over 8192 bytes receive 414 if the
HTTP server has not already rejected the request header. Query decoding happens once and
does not affect path parameters.

The render context inherits cancellation and deadlines from a context already
set by Fiber middleware and is cancelled when matched-route dispatch returns.
Fiber defaults to a background context. The pinned fasthttp request context
does not signal client disconnects; it signals server shutdown instead, and
that signal is not automatically propagated to Fiber's user context. Do not
retain Fiber or fasthttp objects for background work. Retain a route snapshot
and use a separately owned context for work that must outlive the request.
This is route metadata access; the C01 loader contract below can consume it explicitly.

Routes may include multiple single-segment parameters, for example
`users/[userId]/posts/[postId]/page.templ` matches `/users/42/posts/99`.
Fiber selects the route and the server captures each named value separately
for the request. Typed path parameters and query values are available through the route context above. Query strings are not part of captured values. Encoded slashes,
backslashes, dot segments, invalid percent escapes and invalid UTF-8 are
rejected; other encoded values are decoded once after route segmentation.
Fiber's trailing-slash behavior is retained, and repeated interior slashes
do not match a single dynamic segment.

### Catch-all routes

A catch-all segment captures one or more path segments using `[...name]`, for
example `docs/[...slug]/page.templ` matches `/docs/a`, `/docs/a/b`, and
`/docs/a/b/c`. The captured value is the slash-joined decoded segments, stored
under the declared name (`slug == "a/b"`). A catch-all must be the **last** path
segment; a non-terminal declaration such as `docs/[...slug]/extra/` is rejected.
Optional catch-alls (`[[...slug]]`) and zero-segment matches are not supported.

### Route precedence and conflicts

Routes register in a deterministic order independent of discovery order. At the
first differing path segment, a static segment wins over `[name]`, which wins
over `[...name]`; parameter names do not affect priority. For example,
`/products/new` beats `/products/[id]`, and `/docs/[id]` beats
`/docs/[...slug]` for one segment. A static prefix also wins earlier in the
comparison: `/a/static/[...rest]` wins over `/a/[id]/[name]` for
`/a/static/x`. Matching is case-sensitive and the existing trailing-slash
behavior is retained. Non-ASCII static paths register in escaped form so they
match browser URLs.

Equivalent declarations such as `/users/[id]` and `/users/[name]`, including
ones using different render modes, are rejected with both source paths. The
same applies to equivalent catch-alls. As a conservative limit, nested
catch-alls and mixed or multi-segment dynamic routes beneath a compatible
catch-all prefix are rejected. Overlaps involving a static segment with a
reserved ASCII character are also rejected when its encoded spelling could be
captured by the competing dynamic route. Static directory names containing Fiber
route metacharacters (`:`, `*`, `+`, `?`, or backslash) are rejected. For
other reserved characters, Fiber may not match an encoded spelling to its
literal static route. Static descendants and the immediate
single-segment dynamic sibling are supported. These rules apply equally to SSR,
CSR, and navigation requests; the nearest visual layout remains in effect.

Server-rendered catch-all pages read the captured value from the render context
via `routecontext.FromContext(ctx)` and `route.Params.Get("slug")`. Client-rendered catch-all pages mount by stable route id (the declared
module, not the concrete URL) and read the destination from the page root's
`data-go-route` attribute. Encoded slashes, backslashes, dot segments, invalid
percent escapes and invalid UTF-8 are rejected in catch-all values, exactly as
in single-segment parameters.

## Server-side loader contract (C01)

`go-meta/loader` defines `Loader[T]`, a synchronous function receiving a
standard `context.Context` and the matched `routecontext.Route`, and
`loader.Execute`, which invokes it explicitly and returns the same typed value
and error. For example:

```go
import (
    "context"
    "go-meta/loader"
    "go-meta/routecontext"
)

type User struct { ID int64; Tab string; Tags []string }

func loadUser(ctx context.Context, route routecontext.Route) (User, error) {
    id, err := route.Params.Int64("userId")
    if err != nil { return User{}, err }
    return User{ID: id, Tab: route.Query.Get("tab"), Tags: route.Query.Values("tag")}, nil
}

// Inside matched-route processing, obtain route from routecontext.FromContext(ctx).
// user, err := loader.Execute(ctx, route, loadUser)
```

A loader can return any Go type, including a slice or a zero value; a nil error
is distinct from an error. `Execute` calls it once, checks an already-cancelled
context before calling, and preserves its result and error verbatim. A nil
context or loader produces `loader.ErrNilContext` or `loader.ErrNilLoader`.
Cancellation during the call is cooperative: the loader must pass its context to
blocking work and return before dispatch finishes. `Execute` does not start a
goroutine, retry, cache, or override a result after cancellation.

The caller must pass the snapshot belonging to the supplied context. Route
metadata and query values remain safe to retain; the request context is
cancelled when dispatch returns. It inherits parent cancellation and deadlines
when supplied, but this Fiber stack does not guarantee cancellation when a
browser disconnects. Background work needs an independently owned context;
never retain Fiber or fasthttp request objects. Applications must synchronize
shared state captured by a loader.

`ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`, and `ErrInvalidInput` are
optional error classifications usable with `errors.Is`; ordinary and wrapped
errors, including their causes, remain intact. Do not send raw internal errors
to clients. The HTTP and navigation mappings for these sentinels, plus the
`Redirect` outcome, are defined in
"Loader failures, redirects, and cancellation (C05)". C02 registration adapts a
typed loader at the runtime boundary; C03 connects typed data to templ and runs
SSR loaders automatically (see "Executing SSR loaders (C03)"); C04 runs CSR
loaders and serializes their results to JSON for clients (see "Executing CSR
loaders (C04)").

## Registering loaders (C02)

Declare bindings in `app/loaders/bindings.go`, a Go-safe package outside any
bracket-named directory. Use the page's source path relative to `app/` as its
stable ID, including `page.templ` or `page.svelte`:

```go
package loaders

import (
    "context"
    "go-meta/loader"
    "go-meta/routecontext"
)

type UserData struct { ID int64 `json:"id"` }

func loadUser(_ context.Context, route routecontext.Route) (UserData, error) {
    id, err := route.Params.Int64("userId")
    return UserData{ID: id}, err
}

func Bindings() []loader.Binding {
    return []loader.Binding{
        loader.Bind("users/[userId]/page.templ", loadUser),
    }
}
```

The same mechanism supports Svelte pages and catch-alls, for example
`dashboard/page.svelte` and `docs/[...slug]/page.templ`. Concrete URLs such as
`/users/42` are not registration keys. At startup the application builds an
immutable registry against the generated route IDs; duplicate, empty, unknown,
and nil bindings fail with an error. A known route without a binding simply has
no loader. `Registry.HasRoute` distinguishes that case from an unknown ID;
`Registry.Lookup` is safe concurrently and never executes a loader.
The application stores the validated registry for future dispatch integration.

`loader.Bind` checks each function's Go result type at the call site and adapts
it once to a runtime handle, allowing distinct result types in the same
registry. `Registered.Execute` returns a type-erased value and the original
error, and requires the matching route snapshot; C03 uses it as the dispatch
boundary to run SSR loaders and deliver typed data to templ. Registration and
lookup never run loaders, and C02 adds no loader data to SSR or navigation
responses. C03/C04 run loaders and deliver their results (typed for SSR, JSON
for CSR).

`meta dev` rebuilds after edits to this ordinary Go package. Route creation,
rename, and deletion still use the route reconciler and generator. A binding
whose route was renamed or removed fails startup with its old ID until corrected
or removed; duplicate and unknown bindings likewise fail startup, rather than
silently selecting one. Missing Go symbols fail compilation. Fixing the Go
source triggers another rebuild. There are no generated loader imports or
loader-specific generated artifacts to clean up.

## Executing SSR loaders (C03)

For every matched **SSR** request, the framework now runs the route's registered
loader **exactly once, before the page renders**, and makes its typed result
available to the templ page and layout. CSR routes are not touched: a registered
CSR loader is never executed by C03 (that is C04).

A loader is defined and registered exactly as in C02. The result reaches templ
through the `go-meta/loaderdata` package, which mirrors `routeparams` and
`routecontext`: a private context key, no Fiber or fasthttp dependency, and a
strongly typed read.

```go
// app/posts/[postId]/page.templ
package posts

import (
    "fmt"

    "go-meta/app/loaders"
    "go-meta/loaderdata"
)

templ Page() {
    {{
        post, ok := loaderdata.Get[loaders.PostData](ctx)
        var idText, missing string
        if ok {
            idText = fmt.Sprint(post.ID)
        } else {
            missing = "No loader data for this route."
        }
    }}
    <main>
        <p data-post-id>{idText}</p>
        <p data-post-missing>{missing}</p>
    </main>
}
```

`loaderdata.Get[T](ctx)` returns the result only when its dynamic type is
exactly `T`; `ok` is true for a valid result (including a zero value or a nil
pointer of type `T`) and false otherwise. It never panics. `loaderdata.Has(ctx)`
reports whether any loader ran, so a page can tell "no loader for this route"
apart from "a loader ran but I asked for the wrong type". Retrieving a type the
loader did not return (even one with identical fields) reports `ok=false`; there
is no implicit coercion and no developer-written `.(T)` assertion.

The loader runs with the B05 request-lifetime context and the same route
snapshot the page reads, so `routecontext.FromContext(ctx)`,
`routeparams.FromContext(ctx)`, and `loaderdata.Get[T](ctx)` all coexist in one
component. The result is attached to the render context (not the pooled request
object) and is released with the request; it never leaks across requests.

Both SSR response modes carry the rendered data: a full-document request returns
the rendered HTML, and a navigation-envelope request returns the same rendered
HTML inside the existing version-1 envelope (no new `data` field, no version
bump). A route with no registered loader renders exactly as before.

**Failure behavior.** If a loader returns an error, the request stops before
rendering. The full status mapping, redirect handling, and logging policy are
defined in "Loader failures, redirects, and cancellation (C05)": the C01
sentinels map to `404`/`401`/`403`/`400`, a redirect outcome to `302`,
cancellation to `499` (not logged), a deadline to `504`, and anything else to a
generic `500` whose real cause is logged server-side. No raw internal error text
is ever sent to the client.

**What C03 does not do.** No CSR execution or transport (C04), no JSON
serialization of results, no caching, prefetching, retries, or concurrent
multi-loader orchestration, no streaming SSR, and no redirect/status mapping
(C05). The loader is never invoked at startup, registration, code generation, or
reconciliation — only during a matched SSR render.

## Executing CSR loaders (C04)

For every matched **CSR** request, the framework runs the route's registered
loader **exactly once**, JSON-serializes its result, and hands it to the Svelte
page **before it mounts** — on both a direct full-document load and a soft
navigation. SSR routes are untouched: they keep C03's typed `loaderdata` delivery
and are never JSON-serialized into the envelope.

A CSR loader is defined and registered exactly as in C02/C03. The result reaches
the page as a **`data` prop** (the simplest mechanism compatible with the Svelte 5
mount code). The Go side serializes with `encoding/json`; the client reads the
value through the `frontend/loader-data.ts` boundary, which keeps the transport
type as `unknown` and lets each page declare its own expected type.

```go
// app/loaders/dashboard.go
type DashboardData struct {
    TeamID   string `json:"teamId"`
    Title    string `json:"title"`
    Greeting string `json:"greeting"` // not derivable from the URL
}

func LoadDashboard(ctx context.Context, route routecontext.Route) (DashboardData, error) {
    teamID, _ := route.Params.Get("teamId")
    return DashboardData{TeamID: teamID, Title: "Team " + teamID, Greeting: "Loaded by Go"}, nil
}
```

```go
// app/loaders/bindings.go
loader.Bind("dashboard/[teamId]/page.svelte", LoadDashboard),
```

```svelte
<!-- app/dashboard/[teamId]/page.svelte -->
<script lang="ts">
  let { data } = $props<{ data?: { teamId: string; title: string; greeting: string } }>();
</script>
{#if data}
  <h1>{data.greeting}: {data.title}</h1>
{:else}
  <p>No loader data for this route.</p>
{/if}
```

**When it executes.** Once per direct full-document request and once per
navigation-envelope request, before the response is published. It does not run on
static assets, invalid or unmatched paths, unsupported methods, or during
registration/generation. A route with no registered loader keeps the existing
shell (no data script is embedded).

**Transport.** The serialized JSON travels in one consistent place for both paths:
- **Direct load:** embedded in the shell as a `<script
  type="application/json" id="go-meta-csr-data">` element, read by the
  bootstrap before the page mounts.
- **Soft navigation:** an optional `data` field on the existing **version-1**
  navigation envelope (no version bump, no new required fields).

`encoding/json` HTML-escapes `<`, `>`, and `&` by default, so the embedded bytes
cannot close the `<script>` element — a payload containing `</script>` is emitted
as `</script>` and cannot break out. The client never executes the
payload as JavaScript; it is parsed as JSON.

**Absent vs `null`.** A route with no loader omits the `data` field entirely. A
loader that returns a JSON `null` produces an explicit `"data": null`. The client
distinguishes the two: `loaderDataPresent(env)` is false for an absent field but
true for `null`, so a page can tell "no loader" apart from "a loader returned
null".

**Persistent layouts.** Nearest-layout-wins is unchanged. Navigating between
sibling CSR routes under the same layout preserves the layout instance and only
updates the page (and its fresh `data` prop); leaving the subtree disposes the
layout. Loader data is delivered per page mount, so a retained layout never shows
stale page data.

**Cancellation and stale navigation.** The loader runs with the B05
request-lifetime context. A browser `AbortController` cancels the *fetch
consumption*, but that does not guarantee the server cancels the in-flight loader
(Fiber does not wire client disconnects into the request context). The existing
monotonic navigation IDs, abort handling, and serialized commit queue are
preserved: a stale navigation's data is never mounted, applied to props, or
committed to history, because the client only consumes a response after the
freshness check.

**Failure behavior.** A loader error or a JSON serialization failure (which
would prevent a successful publication with partial data) stops the request
before the page is published: no loader data is embedded or placed in the
envelope. The status mapping and redirect handling are defined in "Loader
failures, redirects, and cancellation (C05)"; a loader error is a non-2xx
response, which the navigation engine treats as a failed navigation
(hard-navigation fallback to the status page).

**What C04 does not do.** No C05 error/redirect mapping, no C06 integration gate,
no caching, prefetching, retries, or concurrent multi-loader orchestration, no
streaming, no server actions, no client-side route matching, no generated
Go→TypeScript types, and no client-side schema validation (pages declare their
type; the transport boundary is `unknown`).

## Loader failures, redirects, and cancellation (C05)

C05 gives every loader outcome a single, predictable HTTP and navigation
behavior. The mapping lives in one place — the loader dispatch
(`internal/httpserver/loader_dispatch.go`) — so SSR and CSR, direct and
soft-navigation requests all behave identically. A loader returns one of the
C01 sentinels, a `loader.Redirect` outcome, a `context` error, or an arbitrary
error; the dispatch classifies it and responds **before the page renders or its
data is published**. A failing loader never leaves a half-rendered page: the
response is a status page (or redirect), never a page with missing data.

**HTTP status mapping.** Classification uses `errors.Is`, so an error *wrapped*
around a sentinel (or a `context` error) maps to the same outcome as the bare
sentinel. A `*loader.RedirectError` is checked first with `errors.As` because it
is a control-flow outcome, not a failure.

| Loader outcome | Status | Logged? | Notes |
| --- | --- | --- | --- |
| `loader.Redirect(target)` | `302 Found` | no | Validated root-relative `Location`; empty body. A successful outcome, not a failure. |
| `loader.ErrNotFound` | `404` | no | Page does not render. |
| `loader.ErrUnauthorized` | `401` | no | |
| `loader.ErrForbidden` | `403` | no | |
| `loader.ErrInvalidInput` | `400` | no | |
| `context.Canceled` | `499` | **no** | Client aborted the request (closed the tab, navigated away). Not a server failure, so it is deliberately not logged. |
| `context.DeadlineExceeded` | `504` | yes | The loader ran past its deadline. |
| anything else | `500` | yes | The real cause is logged server-side; the client sees only a generic message. |

`499` ("client closed request") is the nginx convention; it is not an official
IANA status, so it is named as a constant rather than a bare literal. Every
status page is generic by design — it says what happened at the HTTP level
without revealing which loader failed or why.

**Redirects are root-relative and validated.** A loader redirect must name a
same-origin, path-only target (e.g. `/login`). The dispatch rejects anything
that could take the user off-origin or run a different scheme: absolute URLs
(`https://…`), protocol-relative targets (`//host/…`), a backslash-sneaked
absolute URL (`/\host` normalizes to `//host` in browsers), non-path values
(query-only, fragment-only, relative), and control characters (header
injection). An invalid target is a loader bug, not a client condition: it fails
closed as a `500` with no `Location` header rather than emitting a redirect the
client could follow off-origin. This is the open-redirect / XSS defense for
loader redirects.

**Navigation behavior.** A `302` is a bare response (no body). On a **direct
load** the browser follows it transparently. During **soft navigation** the
client's `fetch` (default `redirect: 'follow'`) follows a same-origin redirect
with the navigation header preserved, so the target returns its normal envelope
and the pipeline applies it as if the user had navigated there directly — no
protocol change is needed. A loader **error** during soft navigation is a
non-2xx response, which the navigation engine treats as a failed navigation and
resolves with the full-document fallback to the destination (the status page),
so the user is never stranded on stale content.

**Cancellation and stale navigation.** The loader runs with the B05
request-lifetime context. A newer soft navigation aborts the previous one's
in-flight fetch (`beginNavigation`), and the monotonic navigation-token model
re-checks currency after every await, so a **stale loader response — including a
stale redirect — can never commit** browser state (DOM, history, title, or
scroll). Client-disconnect cancellation is **deliberately not promised**: this
Fiber stack does not wire a browser disconnect into the request context, so the
server may finish a loader the client has abandoned; the guarantee is that the
client never applies a result it no longer owns.

**What C05 does not do.** No C06 integration gate, no caching, no server
actions, no middleware extensions, and no additional routing syntax. The
navigation envelope stays version-1 and backward-compatible: C05 adds no fields
and no version bump.

## Phase C integration gate (C06)

C06 verifies the whole loader system end to end and closes the gaps the earlier
gates left open. It changed product code in three places:

1. **The navigation-envelope validator dropped `data`** (`validateNavigationResponse`
   in `frontend/navigation.ts`). It rebuilt the validated envelope field by
   field but omitted the optional `data` field C04 added, so every *real* soft
   navigation into a CSR loader route delivered `undefined` page props — while
   full-document loads (which read the embedded shell) worked. The earlier C04
   browser tests could not catch it: their "soft navigation" used a synthetic
   `a.click()` from `page.evaluate`, whose `isTrusted=false` events fall back to
   the full-document path by design (the trust boundary). The validator now
   passes `data` through (still `unknown` at the boundary), and the C06 browser
   suite clicks with a real trusted gesture and asserts the envelope fetch
   happened.
2. **Redirect validation now decodes percent-escapes before checking** for
   control characters and scheme separators (`internal/httpserver/loader_outcomes.go`).
   `%0d%0a` (CRLF header injection) and an encoded backslash before a scheme
   (`/a%2f%2fb` → `//b` after decoding) are now rejected. A double-encoded
   slash (`%252f`) stays allowed deliberately: it decodes to a literal `%2f`
   path segment, which is same-origin; rejecting it would break legitimate
   percent-encoded paths. Dot-segment forms (`/a/../b`) remain allowed because
   the policy is same-origin-only, not path-sanitization.
3. **A dev-suite assertion checked the wrong element id** — it asserted the
   absence of `go-meta-loader-data`, but the real id is `go-meta-csr-data`, so
   the check could never fail. Corrected to the real id.

C06 also added coverage without changing behavior: HTTP-level integration tests
for SSR/CSR parity, route-context/query matrices, envelope/redirect checks at
the real HTTP boundary (`cmd/example/c06_integration_test.go`); a browser suite
for trusted-click data delivery, the three data states (absent ≠ JSON null ≠
value), redirect URL/history correctness, and slow-A/fast-B races
(`tests/browser/c06-integration.spec.ts`); dev-suite tests for editing a loader
from success→error and error→redirect (`tests/development/loader-outcome-changes.spec.ts`);
a codegen no-op mtime test; and a Node-free production smoke script
(`scripts/node-free-production-smoke.sh`).

**The complete lifecycle.**

```text
Go Loader[T] (typed, app-defined)
  → loader.Bind[T] registration (immutable registry, no execution)
  → route match (B02/B03 validated paths) → B05 RouteContext snapshot
  → Registered.Execute (exactly once per matched request)
  → SSR:  loaderdata.Get[T](ctx) → templ render (never JSON-serialized)
  → CSR:  encoding/json → initial shell <script> OR envelope "data" (unknown at boundary)
  → errors/redirects intercepted before render or publication (C05 mapping)
```

Verified properties (each pinned by a named test): loaders run exactly once per
request on both response paths; typed data is available to both page and layout
before render; absent/`null`/value stay distinct end to end; a failing loader
stops rendering with no partial page or bootstrap payload; redirects are
validated root-relative only, and a soft-nav redirect lands on the destination
URL with exactly one history entry; stale envelopes lose races to newer
navigations (data, URL, and title all commit from the winner only).

**Payload size.** The framework imposes **no payload-size limit** on loader
results. `encoding/json` streams into the response buffer (no unbounded
intermediate accumulation beyond the encoder's own buffering), and both
transport paths (shell embed and envelope field) use the same serialization.
An application returning unbounded request-derived data can produce very large
documents — sizing and bounding payloads is the application's responsibility.
Practical recommendation: aggregate and truncate in the loader (SELECT counts,
LIMITed lists), not post-hoc. Introducing a framework limit now would be a
breaking change; it can be revisited if a real memory problem appears. Caching,
streaming, and prefetching remain out of scope (Phase E or later).

**Known limitations.**

- Reactive loader data in *persistent layouts*: layout components receive no
  `data` prop; data props belong to pages. A layout wanting per-navigation
  values reads them from its slot content or the route context.
- Client disconnects do not cancel server loaders (Fiber v3 / fasthttp does not
  wire peer disconnect into the request context). The guarantee is that a stale
  result is never committed client-side, not that the server stops work.
- `499` is the nginx convention for client-closed-request, not an IANA status;
  it is emitted as a best-effort terminal status and may not always reach the
  client.
- No caching, singleflight, streaming SSR, prefetching, or generated
  Go→TypeScript types yet. Phase D adds server actions and enhanced forms;
  Phase E adds the cache layer (TTL, invalidation) on top of this contract.

## Toolchain requirements

- **Go 1.26+** is required. The Fiber v3 documentation lists Go 1.26 as the
  minimum version; `go.mod` targets Go 1.26.
- **Node.js / npm** is allowed **only for development and build** (Vite, Svelte,
  TypeScript checks). It is never part of the production runtime: the server is
  a single Go binary serving pre-compiled assets from `dist/client`. Production
  must run with Node.js not installed on the host.

## Generated artifacts policy

## Development

### Requirements

- Go 1.26+ (`go version`) — module toolchain per `go.mod`.
- Node.js with npm (for Vite/Svelte HMR in development only).
- [Air](https://github.com/air-verse/air) for Go hot reload:
  `go install github.com/air-verse/air@latest`
  (looked up in `PATH`, then `$GOPATH/bin`; no second discovery mechanism).
- templ generator via the go.mod tool directive:
  `go get -tool github.com/a-h/templ/cmd/templ@latest`
- npm dependencies: `npm ci`.

Production needs none of these — it is Go-only and serves prebuilt
`dist/client` assets.

### Running

```bash
go build -o bin/meta ./cmd/meta
bin/meta dev
```

`meta dev` prints a compact banner (project root, public URL, which tools
are active), starts the public proxy, Vite, and Air, and prints a single
`Ready: http://localhost:3000` line only after both `/healthz` (through the
proxy → private socket → Fiber) and a Vite module under `/_go_meta_vite/`
actually answer — never before.

Environment:

| Variable | Default | Meaning |
| --- | --- | --- |
| `PORT` | `3000` | Public loopback port browsers use |
| `GO_META_VITE_PORT` | `5173` | Internal Vite port (bound to 127.0.0.1 only) |

Both ports fail with the named service and reason if occupied; `meta dev`
never silently picks another port. The private Fiber backend listens on a
Unix socket inside a session-owned 0700 temp directory removed at shutdown.

What runs:

- Application HTTP: proxy → private Unix socket → Fiber.
- Frontend modules and the HMR WebSocket: `/_go_meta_vite/` → Vite.
- Go edits: Air rebuilds and restarts Fiber; the proxy stays up.
- templ and route metadata: regenerated automatically before each build;
  route additions, deletions, and renames reconcile without restarts.

### Troubleshooting

- **Air not installed** — `meta dev` prints the install command
  (`go install github.com/air-verse/air@latest`).
- **`node_modules` missing** — `cd <project> && npm ci`.
- **Port occupied** — the failing service, address, and reason are printed
  (`address already in use`); set `PORT`/`GO_META_VITE_PORT` or stop the
  other process. `meta dev` never falls back to another port.
- **Go compile error** — Air prints the full compiler diagnostic; the last
  working binary keeps serving until the error is fixed. Fix and save to
  rebuild automatically.
- **templ syntax error** — the templ diagnostic streams through Air's output;
  previously generated artifacts stay valid.
- **Svelte/Vite error** — Vite shows its HMR overlay; save a corrected file
  to recover.
- **Proxy answers 503** — the backend is restarting (Air rebuild). The proxy
  recovers automatically; browsers can refresh shortly. Navigation envelope
  requests get 503 plain text (never a 200 error page).
- **Stale route after filesystem changes** — the periodic reconciler syncs
  within a few seconds; only route files/directories under `app/` matter.
- **Clean shutdown** — Ctrl+C stops Air, Vite, and the proxy, removing the
  private socket directory; repeated start/stop cycles are supported.

The reserved prefixes do not overlap: `/_go_meta_vite/` belongs to Vite
(development only); `/_meta/` (e.g. `/_meta/image`) is application-owned and
always forwarded to Go; unknown routes keep Go's 404 (never a Vite SPA
fallback).

The proxy strips cookies and authorization from Vite requests, rejects
non-local Host headers, and does not expose a configurable upstream. Vite's
filesystem restrictions remain enabled. This is local development software:
do not expose it on a LAN or tunnel it to untrusted users. Remote-device
development is not supported by this configuration.

Run the isolated real-browser development suite with
`npm exec playwright -- test -c playwright.dev.config.ts`.

## Commands (from a clean checkout)

```bash
make generate   # templ codegen — must run before any go build
make build      # generate + go build + npm ci + vite build
make test       # generate + go vet/test + svelte-check + tsc --noEmit
make clean      # remove bin/ and dist/
```

Ordering contract: generation (`make generate`) always precedes Go compilation;
`make build` bundles the full pipeline. Production runs the Go binary and
serves pre-built `dist/client` assets — no Node on the runtime host.

## Status

Tasks A01–A08 of the Phase 1 task board are complete: repository bootstrapped,
the Go module initialized, Fiber v3 (pinned at v3.6.1) added with a
`GET /healthz` endpoint in `cmd/example`, recovery + request-logging middleware
installed, the frontend toolchain initialized (Vite 8 + Svelte 5 + TypeScript,
with `npm run check` via svelte-check), templ v0.3.1100 pinned as a Go
tool dependency (`go tool templ generate`; sample `app/page.templ` compiles
and renders HTML under test), and a Makefile orchestrating deterministic
generate/build/test/clean. Subsequent tasks (route discovery and generation)
are implemented sequentially per the blueprint's gates.

Gate 0 (runnable health endpoint, Svelte build, templ compile) is green.
