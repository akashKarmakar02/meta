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

## Server action contract (D01)

A **server action** is an application-defined *mutation* with typed input and
output. It is deliberately not a loader: a loader reads data for a rendering
request; an action changes application state on an explicit request. D01
defines only the Go contract — the `go-meta/action` package. Nothing in D01
exposes actions over HTTP, registers them anywhere, or executes them outside an
explicit `action.Execute` call.

```go
package app

import (
    "context"

    "go-meta/action"
    "go-meta/routecontext"
)

type CreatePostInput struct {
    Title   string
    Content string
}

type CreatePostResult struct{ ID int64 }

func CreatePost(ctx context.Context, r routecontext.Route, in CreatePostInput) (CreatePostResult, error) {
    if in.Title == "" {
        return CreatePostResult{}, action.ErrInvalidInput
    }
    // persist...
    return CreatePostResult{ID: 42}, nil
}

// result, err := action.Execute(ctx, route, in, CreatePost)
```

`Action[I, O]` is a generic function type — any Go type works as input or
output (structs, pointers, slices, scalars, named types, empty structs), stays
fully typed at the call site, and requires no `any`, type assertions, or
reflection. The package imports only the standard library and `routecontext`;
it has no Fiber, fasthttp, JavaScript, or Node dependency.

**Execution semantics.** `Execute` runs the action synchronously on the
calling goroutine, exactly **once** — exactly-once invocation by the helper,
not exactly-once delivery over a network. It rejects nil contexts and nil
actions (`ErrNilContext`, `ErrNilAction`), refuses to invoke on an
already-cancelled or expired context, and passes input and route through
unmodified (ordinary aliasing rules apply to pointer inputs). Panics are never
recovered. No retries, no caching, no background goroutines, no package-level
state — independent executions are safe to run concurrently.

**Mutation and idempotency.** Actions are **not guaranteed idempotent** —
creating a post twice may create two posts. The framework never retries,
never replays after cancellation, and never rolls back. Cancellation is not a
transaction boundary: a committed side effect stands even if the context is
cancelled afterwards. Transaction boundaries and application-level idempotency
are the action's responsibility. Actions never run implicitly — not during
code generation, startup, registration, route discovery, page rendering,
loader lookup, CSR bootstrap, soft navigation, prefetching, or any ordinary
GET request.

**Route context.** The action receives the B05 `routecontext.Route` snapshot
for its request — stable route ID, declared pattern, typed path parameters
(`r.Params.Int64("postId")`), and query values (`r.Query.Get("notify")`). It
does not reparse URLs or touch Fiber request buffers. `Execute` accepts any
snapshot the caller supplies; D02's transport layer may pass one describing an
action endpoint's own route identity rather than a page route's.

**Cancellation.** Standard `context.Context` semantics: parent cancellation
and deadlines propagate, already-cancelled contexts prevent invocation, and
cancellation during execution is cooperative — `Execute` waits for the action
to return and preserves its outcome.

**Errors.** Ordinary Go errors, compatible with `errors.Is`/`errors.As`,
wrapped causes preserved. The package defines sentinels that parallel the C01
loader set — `ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`,
`ErrInvalidInput` — so both contracts classify with the same vocabulary while
remaining independently versionable. `Execute` converts nothing to HTTP
responses, performs no redirects, and hides no retries.

**Future registration (D02).** Application actions stay typed; a runtime
registry cannot hold generic instantiations directly, but D02 can wrap any
`Action[I, O]` in a closure over the public type — the signature needs no
change. Transport decoding (HTTP bodies, forms) remains separate from business
execution. D03 adds standard HTML form parsing, D04 typed field-validation
errors (extending `ErrInvalidInput` without changing `Action[I, O]`), D05
enhanced client-side submissions, D06 the integration gate.

**Security.** Defining an action exposes nothing. This contract provides no
authentication, authorization, CSRF protection, input validation, rate
limiting, transaction safety, or replay protection. Before any sensitive
mutation becomes browser-reachable, D02+ must restrict endpoints to
appropriate methods, reject cross-origin or unauthorized submissions, define a
CSRF policy, bound request-body sizes, validate untrusted input, avoid
exposing internal errors, and prevent accidental invocation from page loads
or prefetches. D02 delivers the method restriction, origin gate, body limits,
and strict decoding; authentication and token-based CSRF for credentialed
sessions remain the responsibility of later tasks.

## Action registration and HTTP endpoints (D02)

D02 makes actions callable over HTTP. Applications register typed actions
into an immutable registry; the framework exposes them under a reserved,
method-restricted JSON endpoint namespace.

### Registering actions

Define actions in `app/actions` and bind them under stable dot-separated IDs:

```go
reg, err := action.NewRegistry(
    action.Bind("posts.create", CreatePost),
    action.Bind("posts.delete", DeletePost),
)
if err != nil {
    // Malformed IDs, duplicate IDs, or nil actions fail startup with an
    // actionable error rather than silently dropping a binding.
    log.Fatal(err)
}
```

`action.Bind` is fully typed: the input and output types come from the
registered `Action[I, O]`, and type erasure happens once at the transport
adapter boundary. No `any`, reflection, or Fiber dependency appears in
application code.

### Action ID grammar

IDs match `^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`, at most 64 characters:
ASCII letters, digits, underscores, and hyphens in segments separated by
single dots. IDs are case-sensitive and independent of page URLs — several
actions can serve one page, and one action can serve several pages.

### Endpoint format

```http
POST /_meta/actions/{actionID}
Content-Type: application/json
Origin: https://your-app.example.com

{"title":"Hello"}
```

Success returns the action's typed output as JSON:

```json
{
  "ok": true,
  "data": { "id": 42 }
}
```

The example app registers `demo.createPost`, `demo.echo`, `demo.fail`, and
`demo.slow` under this endpoint for testing.

Only POST dispatches actions. GET, HEAD, OPTIONS, PUT, PATCH, and DELETE
never execute an action. The `/_meta/actions/*` namespace is
framework-owned and cannot be shadowed by page routes.

### Input decoding and limits

- Requests must declare `Content-Type: application/json` (charset parameter
  permitted). Anything else — including HTML form content types — is
  rejected with 415.
- Bodies are decoded once, strictly: malformed JSON, trailing values, and
  unknown fields are rejected with 400. Decoding into the registered
  concrete input type uses `encoding/json` with `DisallowUnknownFields`.
- The default request-body limit is 1 MiB, configurable per server via
  `httpserver.ActionEndpointConfig.MaxBodyBytes`; oversized bodies are
  rejected with 413 before invocation. Nothing executes when decoding
  fails.

### Origin and CSRF policy

Every request must originate from the configured trusted origin, compared
by scheme, host, and effective port:

- A request whose `Origin` header is missing, malformed, or does not
  exactly match the trusted origin is rejected with 403.
- No `X-Forwarded-*` or `Host` header can move the trusted origin.
- Configure production origins explicitly via `GO_META_TRUSTED_ORIGIN`
  (for example `https://example.com`). The origin check fails closed when
  unconfigured.
- In development, `meta dev` serves both Vite and Go on one origin
  (`http://localhost:3000` by default), so same-origin requests pass.

This is a real CSRF mitigation for the JSON endpoint (a cross-site page
cannot send a matching `Origin` and cannot use `text/plain` bodies here),
but it is not a complete session-authentication story. The example app has
no cookies or sessions; once authentication exists, a token-based CSRF
mechanism must be added before credentialed mutations are exposed. D03's
form submissions will require a compatible token path.

### Error mapping

| Action result | HTTP | `error` field |
|---|---|---|
| `action.ErrInvalidInput` | 400 | `invalid input` |
| `action.ErrUnauthorized` | 401 | `unauthorized` |
| `action.ErrForbidden` | 403 | `forbidden` |
| `action.ErrNotFound` | 404 | `not found` |
| any other error | 500 | `action failed` |

Error responses use the same envelope: `{"ok":false,"error":"..."}`. The
500 message is generic; internal error details are logged server-side and
never sent to clients. Context cancellation maps to 400 with
`request cancelled`.

### Idempotency and replay

Each accepted request executes its action exactly once. Two valid POSTs
perform two mutations; network retries can duplicate work; cancellation
does not roll back completed effects. The framework performs no retries,
stores no results, and supplies no transactions — idempotency remains an
application concern.

### Actions and loaders stay isolated

Action dispatch never runs page loaders, and page rendering never runs
actions. Navigation envelopes, CSR loader data, and stale-navigation
logic are unchanged from Phase C.

Roadmap: D04 typed validation errors, D05 enhanced client-side submissions,
D06 the integration gate.

## Standard HTML forms (D03)

Actions registered with `action.BindForm` can also be submitted by ordinary
browser-native HTML forms — no JavaScript required. Form submission is
explicit opt-in per action: JSON-only bindings stay JSON-only; form-capable bindings also accept JSON.

### Binding a form action

```go
registry, err := action.NewRegistry(
    action.BindForm("guestbook.submit",
        guestbookSubmit, // Action[I, O]
        func(values url.Values) (GuestbookEntry, error) {
            // decode typed input from form fields
        },
        "/guestbook", // PRG redirect destination after success
    ),
)
```

`BindForm` keeps the same `Action[I, O]` contract; only the transport
differs. The decoder receives every field — repeated keys arrive as
multi-value slices via `values["tags"]`.

### CSRF protection

Forms carry a double-submit HMAC token. The `formcsrf.Manager` (public
package `go-meta/formcsrf`) issues expiring tokens signed with a server
secret; `httpserver.WithFormCSRF` mounts issuance on page GETs, storing the
token in request context where `formcsrf.HiddenField()` renders it as a
hidden form input. The action endpoint rejects any form whose `_csrf` field
does not match the appropriate `_meta_csrf` (HTTP) or `__Host-meta_csrf` (HTTPS) cookie or fails validation against the secret.
Missing, tampered, expired, and cross-site tokens are rejected with 403.

### Post/Redirect/Get

Successful form submissions do not render inline responses. The action
executes once, then the endpoint redirects with 303 to the configured
destination. Refreshing the destination re-issues a GET — the mutation is
not repeated. Structured validation can render the form again; other errors render a generic page without retry semantics.

### Response shape

| Outcome | Status |
|---|---|
| success | 303 redirect to configured location |
| invalid CSRF token | 403 |
| form decoding failure | 400 |
| structured validation failure | 422 |
| body over 1 MiB limit | 413 |
| non-form content type to form action | 415 |
| unknown action or GET | 404 |

The D02 JSON success and generic-error contracts are unchanged; structured validation adds a 422 response.

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

### Validation feedback (D04)

Actions and typed form decoders can return `action.NewValidationError` with a
form message and field messages. Details are copied on construction and when
read. Wrapped errors support `errors.Is(err, action.ErrInvalidInput)` and
`errors.As(err, &validation)` where `validation` is `*action.ValidationError`.
For example:

```go
return CreatePostResult{}, action.NewValidationError(
    "Please correct the highlighted fields.",
    map[string][]string{"title": {"Title is required"}},
)
```

A typed decoder may return the same error before the action runs. Ordinary
decoder failures and malformed requests remain HTTP 400; structured validation
returns 422. Unauthorized, forbidden, not-found, cancellation, deadline, and
internal errors retain their existing mappings. JSON validation responses use
`{"ok":false,"error":"validation failed","message":"...","errors":{"title":["Title is required"]}}`;
`message` is omitted when empty and `errors` is always an object. Success and
other error shapes are unchanged. Only intentionally public validation messages
should be returned; never put internal errors or secrets in them.

For native HTML feedback, attach a renderer and an explicit value allowlist:

```go
action.BindForm("guestbook.submit", SubmitGuestbook, DecodeGuestbookForm, "/guestbook").
    WithFormFeedback([]string{"name", "message"}, guestbookform.Feedback)
```

The callback has type `func(action.FormState) templ.Component` and returns a
complete HTML document. See `app/guestbookform/form.templ` for labels, visible
field errors, `aria-invalid`, and `aria-describedby`. It works without
JavaScript. A binding without a renderer returns a generic 422 page. State is
request-local; `Value` returns the first value, `Values` returns a copy of all
values, and `Errors` returns copies of messages. Only allowlisted names are
available, so passwords, tokens, API keys, authentication codes, payment data,
and file values must not be allowlisted. `_csrf` is always excluded. Names are
literal keys: `user.name` and `items[0]` have no special parsing semantics.
Submitted names containing controls, invalid UTF-8, or more than 256 bytes are
rejected. Render names and messages using templ escaping and explicit markup.

Feedback uses the verified cookie token through `formcsrf.HiddenField`, so a
corrected submission can be sent again; it never reuses an unverified submitted
value. Token-bearing pages and action responses use `Cache-Control: no-store`.
On HTTPS the configured trusted origin selects the Secure, host-only
`__Host-meta_csrf` cookie with `Path=/` and `SameSite=Lax`; HTTP development
uses `_meta_csrf`. Set `GO_META_FORM_SECRET` to the same persistent, secret
32-byte-or-longer value on every production instance and across restarts;
otherwise the per-process random key invalidates tokens after restart and
breaks cross-instance submissions. Configure `GO_META_TRUSTED_ORIGIN` with the
public HTTPS origin even behind a proxy. Actions should validate before
irreversible side effects; an error does not roll them back. Successful native
submissions still use 303 Post/Redirect/Get. D05 remains responsible for
optional enhanced browser submissions.

### Enhanced native forms (D05)

Add `data-go-meta-enhance` to a standard action form, as in
`app/guestbookform/form.templ`. With JavaScript unavailable, it continues to
submit as an ordinary form and uses the D04 HTML feedback and 303 redirect.
The document-level handler also works for dynamically mounted SSR and Svelte
forms; no per-component listener is required. Only same-origin POST forms whose
resolved action is a single valid ID under `/_meta/actions/`, whose encoding is
`application/x-www-form-urlencoded`, and whose target is the current window
are enhanced. Submitter overrides are respected. File inputs, image submitters,
external actions, and unsupported encodings remain native. Browsers lacking
`FormData(form, submitter)` leave submissions with submitters native.

The browser constructs `FormData` with the actual submitter and converts it
to `URLSearchParams`, preserving repeated values and the CSRF hidden field.
It sends a same-origin credentialed POST with `X-Go-Meta-Action: 1` and
`Accept: application/json`. The header requests a response format; it does not
replace the trusted-origin or signed double-submit CSRF checks. A successful
response is `{"ok":true,"redirect":"/guestbook"}` with the registered safe
destination; a 422 uses the validation JSON described above. Existing JSON
action clients remain unchanged.

While pending, the form has `aria-busy="true"` and `data-go-meta-pending`;
additional submits for that form are ignored until the response completes.
On validation, the client inserts text-only field messages and a form summary,
sets `aria-invalid`, and appends error IDs to `aria-describedby`. Errors are
cleared on the next enhanced submission. Form state and requests are isolated;
removed forms abort their fetch and stale results after navigation are ignored.

Success uses a full-document GET to the registered destination, so it does not
preserve a mounted client layout instance. A visible destination link is
provided before navigation as recovery if it cannot start. Neither navigation
failure nor a network error causes an automatic POST retry. A network error
cannot establish whether a mutation committed; inspect the destination or
application state before trying again. Duplicate suppression is per form and
is not server-side idempotency. D05 does not provide automatic loader
revalidation, optimistic updates, uploads, or offline retry; D06 remains the
separate integration and security gate.

### Phase D integration gate (D06)

D06 verified all three transports end to end through one registry and
dispatch pipeline, hardened origin matching, and closed documentation gaps.
Verified behaviors worth knowing when deploying or extending actions:

- **Origin matching normalizes default ports.** `http://host` matches a
  trusted `http://host:80`, and `https://host` matches `https://host:443`;
  scheme and host compare case-insensitively; a genuinely different port,
  scheme, or host still fails closed with 403. Never derive the trusted
  origin from `X-Forwarded-*` headers — configure `GO_META_TRUSTED_ORIGIN`
  to the public origin behind your proxy.
- **Enhanced forms rejected cross-origin receive JSON 403**, not the HTML
  error page, because the client requested JSON processing via
  `X-Go-Meta-Action: 1`. The header itself never bypasses any check.
- **CSRF token replay within the TTL is expected and documented.** The
  signed double-submit token binds authenticity (HMAC over the shared
  secret), not browser identity or single use: a token/cookie pair stolen
  together replays until expiry. Cookie theft mitigation is the deployment's
  HTTPS + `__Host-` prefix policy, not the signature. Set
  `GO_META_FORM_SECRET` to one persistent 32+ byte value across instances
  and restarts; tokens signed by a different secret fail verification, so
  per-process defaults break multi-instance deployments.
- **A committed mutation whose destination navigation fails is never
  retried.** Exactly one action POST occurs; Back/Forward re-issue only
  GETs because the POST never enters session history as an entry. The
  recovery link to the trusted destination stays in the form.
- **Network-level exactly-once semantics are explicitly not promised.**
  Aborting fetch does not cancel a server-side mutation; ambiguous
  outcomes show neutral "outcome unknown" messaging, keep user input, and
  never auto-retry. Actions performing irreversible work should be
  idempotent at the application level.
- **Svelte component forms are fully supported** (see
  `app/d06/form/page.svelte`): client-rendered components read the CSRF
  token from the `_meta_csrf` cookie for their hidden field, feedback is
  isolated per component instance, unmount aborts pending submissions, and
  re-entry remounts cleanly without duplicate listeners.
- **The error matrix is consistent across transports:** validation →
  structured 422 (JSON shape for JSON/enhanced, HTML for native); malformed
  transport input → 400; unauthorized/forbidden/not-found → 401/403/404;
  internal errors → generic 500 with no detail leaked; CSRF failure → safe
  403 (JSON for the enhanced marker, HTML natively).

Phase D does not include automatic loader revalidation after mutations,
cache invalidation, uploads, optimistic updates, or idempotency keys — those
remain future roadmap work (Phases E/G).

## Cache contracts and policies (E01)

Phase E adds caching to go-meta. E01 ships the *contract* only: the public
`go-meta/cache` package defines keys, entries, TTL policies, error categories,
and the `Store` interface. **No storage engine exists yet** — nothing in the
framework caches anything, no loader or action behavior changed, and building
this package adds no runtime component. E02 will implement the bounded
in-memory store over this interface; E03 integrates loaders; E04 adds
invalidation; E05 adds singleflight miss coalescing.

### Public package

```go
import "go-meta/cache"

key, err := cache.NewKey("loader:user-profile", "42", "tab=posts")
policy := cache.Policy{TTL: 5 * time.Minute}
err = store.Set(ctx, key, cache.Entry{Value: data}, policy)
entry, hit, err := store.Get(ctx, key)
err = store.Delete(ctx, key)
```

The package imports only the standard library — no Fiber, fasthttp, templ,
Node, generated registries, or application packages — and is importable by
external Go modules (verified by an isolated external-module test).

### Key identity and namespaces

- `cache.Key` is a string type with `Validate` rules: non-empty, ≤ 2048 bytes,
  valid UTF-8, no ASCII control characters, no edge whitespace.
- `cache.NewKey(namespace, components...)` builds keys with **length-prefixed**
  components, not separators, so distinct component sequences can never collide:
  `NewKey("ns", "a:b", "c") ≠ NewKey("ns", "a", "b:c")`. Empty components are
  distinct from absent ones, and multi-byte UTF-8 is handled exactly.
- The namespace is the first component and the unit of isolation between
  logical domains (`loader:user-profile`, `image:thumbnail`); it is bounded at
  128 bytes and validated like a key. Same components under different
  namespaces never collide.
- E03 owns loader-specific key construction (route identity + params + query +
  scope); E01 provides only the collision-proof primitive.

### Values, ownership, and size

`Entry` holds `Value []byte`. A cached empty or nil value **is a hit**, not a
miss (a serialized JSON `null` is a real value). Byte values were chosen over
`any`/generic storage because CSR loader results already cross a byte boundary
(encoding/json) in this framework, copying semantics are unambiguous, and size
accounting is exact. Implementations must give entries copy-in/copy-out
semantics: after `Set`, caller mutations don't affect the store; after `Get`,
mutating returned bytes doesn't affect future reads. E02 must obey this. E03
will adapt typed SSR loader values to/from bytes at its boundary without
changing `Loader[T]` signatures.

### TTL policy

`Policy{TTL}` is the only knob. TTL is measured from `Set` with a monotonic
clock where available; an entry is served during `[stored, stored+TTL)` and is
a miss at or after expiry. The zero `Policy` is invalid — **caching is strictly
opt-in**: no implicit default TTL exists anywhere, and a loader without an
explicit cache policy executes exactly as in Phase C. Valid TTLs are positive
and at most `MaxTTL` (24h); zero, negative, non-finite, and over-max values
are rejected by `Policy.Validate()` with errors wrapping `ErrInvalidPolicy`.

### Hit / miss / error

`Get` distinguishes three outcomes exactly:

| Result | Meaning |
|---|---|
| `(entry, true, nil)` | Hit — entry present and unexpired; `Value` may be empty |
| `(zero, false, nil)` | Miss — never written, deleted, or expired |
| `(zero, false, err)` | Store error — must not be treated as a miss |

### Concurrency and contexts

Every `Store` implementation must be safe for concurrent `Get`/`Set`/`Delete`
from many goroutines, including on the same key; individual operations are
atomic and visible-when-returned. All methods take `context.Context`: nil
contexts are rejected (`ErrNilContext`), already-canceled contexts return their
own error without side effects, and no operation spawns background goroutines
or retains Fiber request objects. Cancellation after a completed `Set` does not
roll it back.

### Capacity and boundedness

Boundedness is a construction concern of concrete stores. E02 will enforce
entry-count and byte-size limits at construction, reject oversized entries
with `ErrEntryTooLarge`, and own its eviction algorithm. Zero/omitted capacity
values in E02's constructor will be documented errors, not unbounded defaults.
The contract deliberately has no capacity fields — `Policy` describes one
entry's lifetime, not the engine's shape.

### Errors and invalidation

Five sentinel categories wrap via `%w` for `errors.Is`/`errors.As`:
`ErrInvalidKey`, `ErrInvalidPolicy`, `ErrEntryTooLarge`, `ErrClosed`,
`ErrNilContext`. No HTTP meaning attaches to any of them. `Delete` on a
missing key is a successful no-op — exact-key invalidation is idempotent, so
E04 can invalidate speculatively. Namespace- or tag-scoped deletion is not in
the interface; E04 builds any higher-level index on top of exact-key deletes
rather than forcing every backend to support scans.

### Sensitive data and negative caching

A key built only from route ID or URL is **not safe for user-specific results**
— identical URLs do not imply identical authorization. Callers must fold user/
tenant scope into key components (and hash secrets before putting them in a
key). Only successful results are cache candidates: loader failures
(`ErrNotFound`, `ErrUnauthorized`, …), redirects, cancellation, deadline
expiry, and action mutation responses are never cached automatically. No
request context or cookie object is retained in entries (bytes cannot hold
them). Negative caching may arrive later behind its own explicit policy.
E01 implements no authentication or key derivation — the contract is simply
built so that private results are not accidentally shared.

### Known limitations

- No storage exists: `Store` has no implementation in this repo yet (E02).
- `MaxTTL` (24h) intentionally forces long-lived data to be versioned by key
  (content digest) rather than pinned by TTL.
- No namespace-wide invalidation primitive (E04 decides if one is needed).
- Keys are bounded at 2048 bytes; larger identities must digest their payload.

## In-memory TTL cache (E02)

E02 implements the E01 `cache.Store` contract as a bounded, concurrency-safe
in-memory cache: `cache.Memory`, constructed with `cache.NewMemory`.

```go
import "go-meta/cache"

store, err := cache.NewMemory(cache.MemoryConfig{
    MaxEntries:    10000,             // live-entry bound
    MaxBytes:      32 << 20,          // total accounted bytes
    MaxEntryBytes: 1 << 20,           // per-entry bound; 0 = no separate limit
})
if err != nil { ... }
defer store.Close()

key, _ := cache.NewKey("loader:user-profile", "42", "tab=posts")
_ = store.Set(ctx, key, cache.Entry{Value: data}, cache.Policy{TTL: 5 * time.Minute})
if entry, hit, err := store.Get(ctx, key); err == nil && hit {
    // entry.Value is a private copy
}
_ = store.Delete(ctx, key)
```

There is no global singleton and no environment configuration — each store owns
independent state. No loader or action uses this cache yet; constructing one
changes nothing until E03 wires it in.

### Configuration

All limits are required positive except `MaxEntryBytes` (0 disables the
separate limit). There are no implicit defaults: zero or negative values are
construction errors (`ErrInvalidConfig`), as is `MaxEntryBytes > MaxBytes` or a
`MaxBytes` too small for one minimal entry. Both `MaxEntries` and `MaxBytes`
are enforced simultaneously; either may trigger eviction.

### What byte accounting measures

Accounted size is `len(key) + len(value) + 64` bytes of fixed per-entry
overhead. **This is not a strict bound on process RSS or Go heap usage** — map
growth slack, allocator headers, and the runtime's own overhead sit on top of
it. The bound is exact only against the formula.

### Eviction: LRU

Get and Set both refresh recency. When either capacity limit is exceeded on
admission, the store first sweeps expired entries (O(entries), charged to
Sets that need room), then evicts least-recently-used **live** entries until
the new entry fits — never the key being Set. Updates replace in place; they
never create duplicate entries. Eviction is fully deterministic.

### TTL: fixed, not sliding

TTL runs from the successful Set to `stored+TTL`, half-open: at exactly the
deadline the entry is expired and reads as a miss, never stale. Reads never
extend a deadline. Replacement sets a new deadline and value atomically.
Expiry is lazy: an expired entry found by Get is removed and reported as a
miss; entries may stay allocated until some operation touches them — there
is no background sweeper, and physical reclamation follows the Go collector,
not the TTL boundary.

### Ownership and concurrency

Copy-in/copy-out is mandatory and tested: after Set, caller mutations don't
reach the store; after Get, mutating returned bytes doesn't affect future
reads. All operations take one mutex; individual operations are atomic and
readers observe whole entries — old or new, never partial. Safe for
concurrent Get/Set/Delete/Close from many goroutines; verified under the race
detector. Per-process consistency only, not distributed linearizability.

### Contexts, errors, and lifecycle

The error precedence on every operation is: nil context → already-canceled
context → invalid key → invalid policy → closed store → size checks → mutation.
A rejected Set (bad key/policy/oversized/closed/canceled) changes nothing —
including the previous entry under the same key. Cancellation checked before
the critical section prevents every mutation; cancellation observed after the
mutex release cannot undo a completed write (linearization point = mutex
release). `Close()` releases everything, is idempotent and concurrency-safe,
and all later operations return `ErrClosed`. Bytes already handed out by
earlier Gets stay with their callers, and released memory is not zeroed —
values are not encrypted and are not securely erased. There is no reopen;
build a new store.

### Security notes

The cache is process-local. It provides no user/tenant isolation beyond key
namespacing — namespaces prevent key *collisions*, not unauthorized *access*;
correctly scoped keys remain the caller's responsibility (see the E01
section). Values may hold private data; keys may reveal identifiers if logged.
E02 itself caches no errors, redirects, or action responses.

### Handoff

E03 will integrate this store with Phase C loaders without changing
`Loader[T]` — typed values serialize to bytes at that boundary. E04 builds
exact-key invalidation on `Delete`. E05 coordinates concurrent misses outside
the store. Benchmarks (hit 2.6μs, miss 25ns, set 5.4μs, mixed 96B/op median;
full numbers in memory_test.go output) show no pathological behavior: the
dominant costs are the value copy required by the ownership contract and one
clock read per expiry check, which is inherent to any TTL cache on this
machine (time.Now ≈ 2.5μs under this kernel's clocksource).

### Loader caching (E03)

Caching is opt-in per binding. Construct and own one bounded store at
application startup, and pass it to each loader that can safely share results.
The existing `Loader[T]`, `Bind`, and registry APIs are unchanged. For example:

```go
package example

import (
    "context"
    "time"

    "go-meta/cache"
    "go-meta/loader"
    "go-meta/routecontext"
)

type Post struct { Title string `json:"title"` }

func BindPost(store cache.Store, load loader.Loader[Post]) loader.Binding {
    return loader.BindCached("posts/[postId]/page.templ", load, loader.CacheOptions[Post]{
        Store: store,
        Policy: cache.Policy{TTL: time.Minute},
        Codec: loader.JSONCodec[Post](),
        Scope: func(ctx context.Context, route routecontext.Route) (string, bool, error) {
            // Only do this for data that is truly public and identical for
            // every caller. The application must validate private scopes.
            return "myapp-public-v1", true, nil
        },
    })
}
```

Supply the binding to `loader.NewRegistry`, which rejects missing store,
scope or codec and invalid TTL before publishing a registry. Store ownership
and shutdown remain with the application. `cmd/example/main.go` and
`app/loaders/cache_demo.go` show a bounded process-local store shared by two
explicitly public example routes. The cache empties when that process restarts.

The scope callback must affirm that caching is safe for the request and
identify *all* relevant user, tenant, authorization, locale, feature, header,
and application/version dependencies. Return `("", false, nil)` to bypass;
an error fails the request. There is no implicit public or private scope.
Use an application namespace in the scope when stores are shared between
applications. Do not put credentials or session tokens in keys: keys are
readable by the store. A stable identifier or appropriately protected digest
can be used when its privacy and collision properties suit the application.

`loader.LoaderCacheKey(route, scope)` builds the same exact key used by the
binding and can be used with `Store.Delete`. It uses `cache.NewKey` with the
stable route ID, scope, every decoded path parameter and every query value.
Parameter and query names are sorted; repeated query values keep their original
order. Missing, empty and repeated values remain distinct. B05 route snapshots,
not raw URLs, supply the values; malformed queries are rejected by routing.
The store's key-length limit applies. TTL must be positive and at most
`cache.MaxTTL`; hits do not extend it.

The typed codec runs before type erasure, so cache hits restore `T` for templ
and CSR continues using its existing JSON transport. `JSONCodec[T]` is useful
for JSON-round-trippable types, but is not a universal Go serializer: interface
fields, unexported fields, custom marshaling and time representations may lose
information. Before writing, the adapter decodes and checks `reflect.DeepEqual`
against the original; a lossy or failed round trip renders the original result
without caching. A custom `Codec[T]` can be supplied; its implementation must
be concurrency-safe, return owned encoded bytes and independently owned mutable
results, and version its format in the scope when needed. Equality
checks cannot prove the behavior of a stateful or nondeterministic codec.
Decoded cache entries must be trusted as store data, not as authentication.

Only successful loader results are candidates. Errors, redirects and canceled
requests are never cached. An operational read failure bypasses the cache; an
operational write failure or serialization failure returns the original typed
result. Cancellation, invalid keys and invalid policies propagate. A corrupt
entry that fails decoding is deleted and the loader runs once; failed deletion
is reported and a successful write can replace it. Optional `OnEvent` receives
hit, miss, bypass, closed-store and failure categories without keys or values; its callback
must be concurrency-safe. CSR still returns its existing error if its own JSON
serialization fails.

Memory bounds come from `cache.MemoryConfig`, and E03 caches loader data only,
not HTML, CSRF tokens or navigation envelopes. Concurrent misses may run the
loader more than once; there is no coalescing, background refresh, or automatic
action invalidation. E04 handles invalidation orchestration and E05 handles
singleflight. Cache lookup and encoding can cost more than a cheap loader;
measure your workload rather than caching by default.

### Invalidation and revalidation (E04)

Applications invalidate explicitly after a mutation commits. The framework
never guesses which loaders a mutation affects — the action knows which
entries it touched and invalidates exactly those keys.

Create one `loader.Invalidator` per application store at startup and pass it
into every `CacheOptions` for that store:

```go
package example

import (
    "context"

    "go-meta/cache"
    "go-meta/loader"
)

func setup(store cache.Store) (*loader.Invalidator, error) {
    inv, err := loader.NewInvalidator(store)
    if err != nil {
        return nil, err
    }
    // Attach inv to every CacheOptions over this store; bindings whose
    // Invalidator targets a different store are rejected at construction.
    return inv, nil
}
```

An action then commits its mutation first, invalidates second:

```go
func UpdatePost(_ context.Context, route routecontext.Route, in UpdateInput) (UpdateResult, error) {
    if in.ID == "" {
        return UpdateResult{}, fmt.Errorf("action: invalid input")
    }
    commitMutation(in.ID, in.Title) // application mutation; nothing has been invalidated yet

    r := routecontext.Route{RouteID: "posts/[id]/page.templ"}
    r.Params = routeparams.NewParams(map[string]string{"id": in.ID})
    if err := demoInvalidator.Invalidate(context.Background(), r, "app-v1"); err != nil {
        // The mutation stays committed — the framework does not roll it back.
        // The stale entry survives until its TTL expires, or retry (safe).
        return UpdateResult{}, fmt.Errorf("invalidate: %w", err)
    }
    return UpdateResult{OK: true}, nil
}
```

`Invalidate` reuses E03's exact identity: same route ID, decoded params,
query (missing distinct from empty, repeated values ordered) and application
scope. One tenant's scope is a different key, so no tenant can invalidate
another's entry by manipulating a query string. Keys can also be precomputed
with `loader.LoaderCacheKey` and passed to `InvalidateKey`. Query variants are
deliberately distinct keys — an action that renders `?tab=overview` must
invalidate that exact variant too if it cached it.

The critical race — a slow fill reading version 1 while an action commits
version 2 and invalidates, then the fill repopulates version 1 — is fenced:
each fill registers against the key before reading the store, and its
`Store.Set` runs inside the same per-key critical section as the
invalidation's generation advance plus delete. A late fill observes the
advanced generation and its write is suppressed (`ErrStaleFill` internally;
the `stale_fill_suppressed` cache event); the request still receives its own
already-computed result. Fencing requires attaching the invalidator on every
cached binding over the store; a binding without it is unfenced and outside
the guarantee (its writes go directly through `Store.Set`). Raw `Store.Set`
writes outside the loader adapter bypass fencing entirely.

Coordinator state is reference-counted per key and released with the last
in-flight fill; there is no per-key goroutine, timer, or permanent
generation map, and unrelated keys never serialize against each other.
Filling holds the fence's mutex only around a generation check and one store
call — never across loader execution.

Revalidation is lazy: invalidation removes the entry, and the next matching
request misses and executes the loader with a fresh TTL. There is no
background refresh, no stale-while-revalidate, and no automatic browser
update — an already-mounted Svelte page shows old data until its next
navigation or reload. Cancellation does not undo a completed invalidation; a
failed delete still advances the generation (stale fills are suppressed) but
may leave the entry until expiry — retrying invalidation is safe and
idempotent. Invalidating a missing key succeeds as a no-op.

E04 does not implement singleflight: concurrent misses after invalidation each
execute the loader (E05 will coalesce them). Invalidation affects only the
current process's store — no cross-process or distributed invalidation.

### E05: opt-in coalescing of cached loader misses

A process-local `FlightGroup` lets concurrent misses for an explicitly cached
binding share work. Construct it with the same invalidator as the binding:

```go
inv, err := loader.NewInvalidator(store)
if err != nil { return err }
group, err := loader.NewFlightGroup(inv, 5*time.Second)
if err != nil { return err }
binding := loader.BindCached("posts/[id]/page.templ", loadPost,
    loader.CacheOptions[Post]{
        Store: store, Invalidator: inv, Flights: group,
        Policy: cache.Policy{TTL: time.Minute},
        Scope: publicPostScope, Codec: loader.JSONCodec[Post](),
    })
_ = binding
```

The scope callback remains mandatory and must identify every security-relevant
input. Never share a scope between tenants or principals unless their results
are interchangeable. Coalescing is an explicit additional promise: shared work
runs under a fresh, bounded context containing the route snapshot and route
parameters, **not** any request's authentication, transaction, tracing or
other context values. Use ordinary `BindCached` without `Flights` for loaders
that require those values. A group is owned by the application, does not close
its store, and may serve multiple bindings. Only executions of the same binding
and complete E03 cache key can join. Independent bindings remain independent.

Cache hits bypass the group. A miss first joins or creates a flight and the
owner checks the store again before loading, avoiding a second fill if another
request just finished. Each waiter can cancel or reach its deadline independently;
the shared context has the configured maximum duration and is canceled when
all waiters leave. Loaders, stores and codecs must honor cancellation: Go
cannot forcibly stop an uncooperative callback. Work retains no Fiber request
or response objects. No goroutines are started until a miss, and completed
flight metadata is removed. Panic values are hidden behind `ErrFlightPanic`.
Errors and redirects are shared with existing waiters but never cached.

Shared results are encoded once, validated by the existing round-trip check,
and independently decoded for each waiter. Custom codecs must produce
independently owned mutable values on each decode. If encoding or round-trip
validation cannot safely copy a successful result, one waiter receives that
result and other waiters execute independently, preserving successful loader
semantics. Codec failures are not cached. A successful flight attempts one
fenced cache write; ordinary store errors retain the E03 bypass behavior.
`CacheFlightCreated` and `CacheFlightJoined` events report admission without
exposing keys or values, and callbacks run outside coordinator locks.

A flight holds an E04 fence reference until its work exits. Its identity includes
that record, its generation and binding identity. Invalidation linearizes at
the generation bump: a request acquiring its fence after successful
invalidation cannot join the old flight. Requests overlapping invalidation may
still receive their historical result; the old generation cannot write it into
the invalidated cache. Failed invalidation can leave the previous cache entry;
check its error and retry. Cleanup compares the actual flight identity and
cannot remove a replacement. The coordinator is process-local: it does not
coalesce across instances, deduplicate actions, or provide background refresh.
The example's `?slow=1` adds a bounded delay to make concurrent fills visible.

### E06: integration, correctness, security and performance gate

E06 verified E01–E05 as one system and closed with two narrowly scoped fixes:

- **Clock reads in eviction sweeps (`cache/memory.go`).** `reclaimExpired`
  previously read the clock once per entry while holding the store lock. On
  hosts without a working vDSO clock — where `time.Now()` can cost ~2.5µs
  instead of ~25ns — that made every admission-evicting `Set` O(entries ×
  clock) and dominated contention-free writes under pressure. The sweep now
  takes one clock reading per pass; TTL semantics (fake-clock boundary tests)
  are unchanged, and high-cardinality churn improved ~190× on this host.
  The host's slow `time.Now()` remains an environment fact to record when
  comparing benchmark numbers.
- **Pre-route middleware hook in the example app (`cmd/example/main.go`).**
  `newConfiguredAppBase`/`newConfiguredApp`/`newConfiguredAppWithActions`
  accept optional `preRoute` handlers so tests can install request-scoped
  principal middleware exactly as real auth middleware would: before any
  route registration. No framework change.

**Cache-key identity** (§ E03 key builder): route ID, static path segments,
each dynamic parameter (nested or catch-all decoded to its full string), the
canonical query encoding, the application scope, and explicit bypass all
participate. `a/b/c` and a catch-all capturing `["a","b","c"]` on the same
route are the same key by design — same URL, same data.

**Tenant isolation**: two properly distinct scopes over one URL miss and fill
independently, hit independently, and never observe each other's entries; the
framework's responsibility ends at faithfully including everything the
application's scope callback declares. An application that returns a shared
scope for distinct tenants opts into sharing — no framework layer can detect
that mistake. Cache and flight events carry no keys, values, or scope strings.

**Singleflight context contract** (pinned by HTTP-level test): request context
values do not enter flight-owned contexts. A loader whose output depends on a
request-specific value must not use `Flights`; the scope callback itself still
evaluates per-request with the full request context, so tenant-distinct
scopes stay tenant-distinct even while flights are enabled.

**Failed invalidation**: if `Store.Delete` fails after the generation bump,
the old entry may survive. Invalidation returns an error — never a false
success — and retry is safe. Committed mutations are not misrepresented as
rolled back; there is no automatic broad flush or cross-tenant fallback.

**Benchmark reading guide** (`loader/e06_bench_test.go`): compare modes on
one machine only; cached misses on a full bounded store include eviction cost;
burst benchmarks measure wall-clock for N joiners plus total executions (1
with singleflight, N without). On this host (8 vCPU, time.Now ≈ 2.5µs),
cached hits for a small struct cost roughly 4–5µs/op (~2 allocs) — several
clock reads — so caching pays off for loaders costing more than a few
microseconds (DB or network-like work of ≥100µs is decisively positive), and
loses for trivial in-memory loaders. The full environment record and numbers
are in the E06 commit message. No other performance rewrites were made.

### Image optimization contract (F01)

`go-meta/imageopt` defines requests; it does **not** resolve, fetch, decode,
resize, encode, or cache images. The reserved `GET /_meta/image` validates
requests and currently answers `501 Not Implemented` for valid ones. Invalid
queries return `400`; other methods return `405` with `Allow: GET, HEAD`.
`HEAD` has the same status and headers as GET without a body. These responses
are plain text, `no-store`, and never page-navigation envelopes. The endpoint
is registered before page routes and is served through the normal development
proxy and production Go server.

When processing exists, a missing approved source is intended to return 404,
an unsupported source media type 415, a source exceeding limits 413, and an
unexpected internal failure a generic 500. These future outcomes must not
reveal filesystem paths or decoder internals, and processing must finish before
a successful image response is committed.

```go
package main

import (
    "fmt"
    "log"

    "go-meta/imageopt"
)

func main() {
    request := imageopt.Request{
        Source: "/images/hero.jpg", Width: 960, Quality: 80,
        Format: imageopt.FormatAuto,
    }
    imageURL, err := imageopt.URL(request)
    if err != nil { log.Fatal(err) }
    fmt.Println(imageURL) // usable as an img src in templ or Svelte
}
```

Query keys are `src`, `w`, `h`, `q`, and `f`. Unknown, duplicate, and explicitly
empty parameters are rejected. `w` and `h` are decimal integers in 1–4096;
one may be omitted but at least one is required. If both are specified the
future processor fits the image within the box while preserving aspect ratio;
cropping and upscaling policy are not yet implemented. The requested output
box cannot exceed 16,777,216 pixels. `imageopt.Limits` and `ValidateWithin`
allow each server to impose stricter positive bounds; the HTTP adapter checks
its limits before serving. `q` defaults to 80 and ranges from 1 to
100; its visual effect depends on the eventual encoder. `f` defaults to
`auto`; `avif`, `webp`, `jpeg`, and `png` name possible output formats, not
implemented encoders. Explicit defaults and omitted defaults are equivalent.
`ParseRawQuery` decodes exactly once, while `ParseValues` accepts already
query-decoded values. `Request.Validate` validates direct construction;
`URL` validates and percent-encodes in deterministic parameter order.

Sources are public URL identities under `/images/` or `/assets/`, not filesystem
paths. Namespace membership does **not** grant filesystem access: a future
resolver must explicitly bind each namespace to an approved public root and
verify containment after resolving symlinks. Controls, invalid UTF-8, dot or
empty segments, backslashes, percent signs remaining after query decoding,
queries, fragments, and scheme syntax are rejected. Remote, `file:`, and
protocol-relative URLs are not accepted. A future remote resolver needs an
explicit allowlist, DNS-rebinding protection, private/link-local address checks,
redirect and port validation, bounded downloads, timeouts, and response-type
verification; none is implemented here. Private or personalized images and
credentials must not be passed to a public optimizer or shared cache.

F03 must negotiate `auto` only among actually available and allowed encoders,
using HTTP Accept media ranges and quality weights, honoring `q=0` exclusions;
server preference breaks equal weights. A missing Accept permits any available
format, wildcards cover their matching media types, and explicit formats do not
use Accept to select a different format. If no allowed format is acceptable,
the future endpoint must fail rather than mislabel bytes. Malformed Accept
values must not be treated as proof of support. Negotiation is not
implemented in F01. Future successful responses must use the selected format's
MIME type and correct body length when known, and auto responses must include
`Vary: Accept`. Cache-Control must reflect source privacy; ETag and conditional
requests are not implemented or promised by F01.

`CacheIdentity.Key` is a deterministic, length-delimited, versioned digest of
namespace, source, source version, requested dimensions, contain behavior,
quality, selected **concrete** format, and processor version. A trusted future
resolver must supply a source fingerprint/version that changes with the source,
and a processor version that changes with relevant encoding behavior. Raw
paths never become cache filenames. F05 may evolve the storage format without
changing the public URL. F01 creates no disk cache.

F02–F04 must enforce source-byte, decoded-pixel, memory, output-byte, time, and
concurrency limits at the real I/O and decoder boundary; compressed byte size
alone does not prevent decompression bombs. SVG rasterization is unsupported
without separately reviewed sandboxing; normal static SVG serving is unchanged.
Animated inputs must be rejected until preservation is explicitly supported,
not silently reduced to one frame. Frame counts and duration require bounds,
and decoder dependencies must be maintained. SSR and CSR may use the same
root-relative URL, and production continues to require no Node runtime.
