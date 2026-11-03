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
to clients. C05 will decide HTTP and navigation mappings and redirect handling;
no redirect outcome is defined here. C02 registration adapts a typed loader at the runtime boundary; C03 can connect
typed data to templ, and C04 can define serialization. None of those rendering
integrations or automatic execution is implemented yet.

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
registry. Explicit `Registered.Execute` returns a type-erased value and the
original error, and requires the matching route snapshot; it is intended as the
future integration boundary. Registration and lookup never run loaders, and C02
adds no loader data to SSR or navigation responses. C03 will decide how templ
receives data; C04 will decide what may be serialized for clients.

`meta dev` rebuilds after edits to this ordinary Go package. Route creation,
rename, and deletion still use the route reconciler and generator. A binding
whose route was renamed or removed fails startup with its old ID until corrected
or removed; duplicate and unknown bindings likewise fail startup, rather than
silently selecting one. Missing Go symbols fail compilation. Fixing the Go
source triggers another rebuild. There are no generated loader imports or
loader-specific generated artifacts to clean up.

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
