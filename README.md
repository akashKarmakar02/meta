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
This is route metadata access, not the future Phase C loader API.

Routes may include multiple single-segment parameters, for example
`users/[userId]/posts/[postId]/page.templ` matches `/users/42/posts/99`.
Fiber selects the route and the server captures each named value separately
for the request. A public page-data and typed-parameter API is deferred to
B04/B05. Query strings are not part of captured values. Encoded slashes,
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

Precedence is deterministic and does not depend on filesystem enumeration
order: static routes are never shadowed by a catch-all, and a single-segment
dynamic route directly under the catch-all prefix (`/docs/[id]` + `/docs/[...slug]`)
is the documented one-segment overlap — the dynamic route wins for exactly one
segment and the catch-all wins for two or more. Conflicts that Fiber cannot
resolve deterministically (a nested catch-all, or a multi-segment dynamic route
under a catch-all prefix) are rejected at generation time.

Server-rendered catch-all pages read the captured value from the render context
(`ctx.Value("slug")`); the typed, query-aware parameter API is deferred to
B04/B05. Client-rendered catch-all pages mount by stable route id (the declared
module, not the concrete URL) and read the destination from the page root's
`data-go-route` attribute. Encoded slashes, backslashes, dot segments, invalid
percent escapes and invalid UTF-8 are rejected in catch-all values, exactly as
in single-segment parameters.

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
