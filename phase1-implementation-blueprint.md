# Phase 1 Implementation Blueprint — Go + Fiber v3 + templ + Svelte 5

**Status:** implementation specification, not implemented.  
**Audience:** small coding agents (e.g. Qwen 27B), independent contributors.  
**Scope:** file routing, server rendering, client rendering, layout ownership, and soft navigation.  
**Explicitly out of scope:** RPC/codegen APIs, database, images/libvips, Valkey, caching policies beyond static assets, auth, deployment orchestration, custom templating language, full production CLI.

## 0. Binding decisions — do not reinterpret

1. Runtime server: Go + **Fiber v3** (`github.com/gofiber/fiber/v3`), `fiber.Ctx` **interface**, not `*fiber.Ctx`. Pin compatible versions. Current Fiber v3 documentation states Go **1.26+** minimum; verify before implementation. Use `go version` and `go mod tidy` to test.
2. templ is Go SSR. Svelte 5 is **client only** in this framework, no Svelte SSR or server JS runtime.
3. Node/npm/Vite allowed **only** for development and build; production serves Go plus compiled assets.
4. Rendering mode by exact filenames: `page.templ` => SSR; `page.svelte` => CSR; `page.static.templ` => reserved for later phase. Never infer mode from unrelated component files.
5. Hybrid means **templ page with a separately mounted client Svelte island**, not transpiling Svelte into Go.
6. No SPA requirement for the MVP bootstrap: browser reload must work first; then add progressive enhancement and back/forward.
7. First iteration: **one root `layout.templ`**. Add nested `layout.templ` (SSR) and nested `layout.svelte` (CSR) after basic pages work. Distinct layout technologies may not blindly wrap each other. Use explicit ownership rules below.
8. Route generation occurs in a dev/build command, **not on every request**. The Go runtime registers generated code; Go imports of templ-generated components must be real compiled symbols.
9. No automatic JavaScript SSR for Svelte. A direct request to `/dashboard` receives a complete HTML document with a `<div>` mount point and necessary assets; actual dashboard content appears after JS starts.
10. Browser native semantics win. External links, downloads, modified clicks, `target`, `mailto:`, fragment-only links should not be hijacked.
11. Never use Fiber request-backed values or `fiber.Ctx` beyond the handler lifetime. For rendering, prefer `c.Context()` as a standard `context.Context` where appropriate and copy any needed mutable Fiber data.
12. **Do not claim instant no-reload navigation until verified** in a browser with a navigation counter, browser history, and state persistence assertions.

## 1. Golden path demonstration

The demo must have:

- `/` templ home; server HTML shows `Welcome` with JavaScript disabled.
- `/about` templ page; server HTML shows `About` with JS disabled.
- `/dashboard` Svelte client page with clickable counter.
- `/dashboard/settings` nested Svelte page.
- `/blog` reserved static templ filename; initially choose either exclude with documented error or implement static build later; Phase 1 should **exclude** it from generated routes and report `STATIC_NOT_IMPLEMENTED` at build time if present. Do not silently SSR it.
- `/products/42` optional **after** static routes work; demonstrate dynamic parameter generation.
- Templ navigation `/` → `/about` → `/dashboard` → `/dashboard/settings` → `/` without document reload after navigation runtime is enabled.
- Direct browser navigation/reload at `/dashboard/settings` works (Fiber returns complete shell).
- Root shared navbar remains structurally stable where possible. A counter in the dashboard's persistent Svelte layout remains unchanged when navigating within the dashboard; it may reset when navigating out of the dashboard (layout subtree unmounted).
- Non-JS links always work with full reload fallback.
- `go test ./...`, frontend `npm run check` and `npm run build` pass.

## 2. Directory structure (target by end of Phase 1)

```text
repo/
  go.mod
  go.sum
  package.json
  package-lock.json
  vite.config.ts
  tsconfig.json
  svelte.config.js                  # only if toolchain requires it
  cmd/
    example/main.go                # Fiber executable
    routegen/main.go               # calls generator to write generated Go + TS
  internal/
    routing/
      schema.go                     # route and layout descriptor types
      discover.go                   # scan app tree
      normalize.go                  # URL normalization
      validate.go                   # collision and invalid file rules
      discover_test.go
    codegen/
      generate.go                   # generation of route registration
      templates/                   # safe templates for generated code
      generate_test.go
    httpserver/
      server.go                     # Fiber construction, routes, error handling
      response.go                   # full HTML vs navigation response
      assets.go                     # manifest reader and static file paths
    render/
      document.go                   # full document wrapper
      templ.go                      # SSR component render adapter
      svelte.go                     # CSR mount shell
      layouts.go                    # layout chain model and renderer
  app/
    layout.templ
    page.templ
    about/page.templ
    dashboard/layout.svelte
    dashboard/page.svelte
    dashboard/settings/page.svelte
  frontend/
    entry.ts                        # starts navigator and bootstraps page
    router.ts                       # same-origin soft navigation
    route-registry.generated.ts     # generated import() map
    svelte-mounter.ts               # mount/unmount operations
    types.ts                        # navigation protocol types
    styles.css
  generated/
    routes_gen.go                   # generated; do not edit
  public/
    favicon.ico                    # optional
  dist/
    client/                        # generated JS/CSS and manifest
  tests/
    browser/navigation.spec.ts     # Playwright, later microtask
  docs/
    routing-contract.md
    rendering-contract.md
    navigation-protocol.md
    limitations.md
```

Note: Go package paths and exact app/generated ownership may be adjusted **once** during initial scaffolding. Keep all generated outputs stable and deterministic. A package in `generated/` cannot import an app package if app imports generated; avoid cyclic import direction.

## 3. Precise route discovery rules (Phase 1)

### 3.1 Allowed page names

- `page.templ` -> SSR
- `page.svelte` -> CSR
- `page.static.templ` -> reserved; emit explicit not-supported error for now
- `layout.templ` -> SSR layout
- `layout.svelte` -> CSR layout
- `*.templ` other than page/layout -> component, not route
- `*.svelte` other than page/layout -> component, not route

### 3.2 URL normalization

- `app/page.templ` => `/`
- `app/about/page.templ` => `/about`
- `app/dashboard/settings/page.svelte` => `/dashboard/settings`
- Preserve original display URL in the address bar where possible, but canonicalize internal match paths using these rules: remove query and hash for lookup; collapse trailing slash except `/`; URL percent-decoding/security checks handled separately; do not allow traversal from untrusted URL path.
- URL query strings and hash stay preserved across client navigation as appropriate.
- Static segments exact match, case-sensitive (`/About` != `/about`).
- Don't implement group segments `(marketing)`, catch-all `[...rest]`, optional segments, or parallel/intercept routes in Phase 1.

### 3.3 Dynamic route optional second pass

- `app/products/[id]/page.templ` => `/products/:id` for Fiber only AFTER explicit translator is implemented.
- Reject ambiguous duplicates such as `/products/[id]` and `/products/[slug]`.
- Static route `/products/new` must take precedence over `/products/:id`.
- Reject malformed brackets and `..` segments.
- Output deterministic ordered descriptors.

### 3.4 Collision handling

- `page.templ` and `page.svelte` in same folder => **build failure** with path and actionable error.
- `layout.templ` and `layout.svelte` in same folder => **build failure** for Phase 1. Do not guess layout precedence.
- Duplicate normalized route => build failure.
- Invalid root path or escaping symlink => build failure (do not walk outside app root).
- Empty app folder => error with actionable message.
- Files ignored: hidden folders, `node_modules`, `dist`, `.git`, `generated`; when present under app report or skip deterministically.

### 3.5 Data model

```go
type RenderMode string
const (ModeSSR RenderMode = "ssr"; ModeCSR RenderMode = "csr")
type Route struct {
    Path string          // e.g. "/dashboard/settings"
    SourceFile string    // slash-normalized relative path
    Mode RenderMode
    Layouts []Layout     // outermost -> innermost
    ID string            // deterministic stable identifier, not random
}
type Layout struct {
    ID string
    SourceFile string
    Mode RenderMode
}
```

The scanner describes files; it must not attempt to compile Svelte or execute templ files. Generated code and Vite consume the same manifest contract.

## 4. Renderer and layout ownership contract

### 4.1 SSR full document

- GET `/about` => status 200, `text/html; charset=utf-8`, HTML5 document with doctype, `<html lang="en">`, `<head>` title, CSS/JS assets, body containing server-rendered templ content and nav mount data.
- Rendering occurs inside Fiber handler through compiled templ components. Use `templ.Component.Render(ctx, io.Writer)`.
- Buffer output into a `bytes.Buffer` during Phase 1 to simplify failures and avoid partial responses; benchmark/streaming deferred.
- Error in render => 500, no partial HTML, structured log.
- All dynamic user data use templ escaping; no unsafe HTML injection.

### 4.2 CSR full document

- GET `/dashboard` => complete HTML document and meaningful metadata; contains explicit app mount point `data-go-page-root`, route ID, and scripts/CSS built by Vite.
- The Go server never executes Svelte code.
- `frontend/entry.ts` imports generated page loader using `import()` and Svelte 5 `mount`, attaching to the mount element.
- A page navigation away calls Svelte 5 `unmount` and awaits async cleanup as needed.
- Do NOT use legacy `new Component(...)` or `$destroy()` patterns from Svelte 3/4.

### 4.3 Nested layouts — incremental scope

- First SSR root `layout.templ` wraps `/` and `/about`.
- First CSR `dashboard/layout.svelte` wraps dashboard child pages with navbar/sidebar/counter state.
- Important design nuance: a Svelte layout cannot be rendered inside Go SSR as a compiled server component. Keep Svelte subtree inside a Go-produced DOM mount element. Root Go layout owns document & header; nested Svelte layout owns its client DOM subtree.
- At `/dashboard` → `/dashboard/settings`, keep the existing Svelte `dashboard/layout.svelte` **instance** alive; swap only its nested child. This requires an explicit outlet/route-store mechanism or component host. Do NOT remount the layout on every child route and falsely claim persistence.
- At `/dashboard` → `/about`, destroy the Svelte layout tree; root HTML wrapper remains where applicable.
- Nested SSR-only layouts are a separate mini-milestone: `app/docs/layout.templ` can wrap `/docs` pages on the Go side via typed composition. Avoid runtime parsing of templ source.
- Mixing an SSR nested layout beneath a CSR layout is **unsupported in Phase 1**; surface clear error. Likewise any hierarchy not explicitly defined. This is a deliberate restriction, not a silent fallback.

## 5. Navigation contract (fixed before building router)

### 5.1 Request/response wire format

Normal browser GET: `Accept: text/html`, no special header -> full HTML document.

Soft navigation GET: request header `X-Go-Web-Navigation: 1`, Accept `application/json` -> JSON envelope:

```json
{
  "version": 1,
  "url": "/about",
  "routeId": "about/page.templ",
  "mode": "ssr",
  "title": "About",
  "layouts": ["app/layout.templ"],
  "html": "<main>...</main>",
  "clientEntry": null
}
```

For CSR:

```json
{
  "version": 1,
  "url": "/dashboard/settings",
  "routeId": "dashboard/settings/page.svelte",
  "mode": "csr",
  "title": "Settings",
  "layouts": ["app/layout.templ", "app/dashboard/layout.svelte"],
  "html": "",
  "clientEntry": "dashboard/settings/page.svelte"
}
```

- JSON is **not prebuilt HTML executable content**; only server-owned templ output is eligible for HTML insertion. This is a same-origin trusted-server protocol, not arbitrary third-party HTML.
- The browser maps `clientEntry` to a **generated static import registry** (no arbitrary path import from server response).
- `layouts` is used to find longest shared prefix. Version check: unsupported version triggers a normal full page navigation.
- `routeId` and `clientEntry` are logical stable keys, not URLs to evaluate.
- If a route responds with redirect, normal navigation semantics apply. For soft navigation, follow same-origin redirects; cross-origin redirect => hard browser navigation.
- `Cache-Control: no-store` for navigation JSON in Phase 1. HTTP caching optimization later.
- API names/headers are **framework proposals**; implement tests against this contract before implementation.

### 5.2 Browser navigation algorithm

1. Install document-level listener on trusted same-origin anchor clicks.
2. Ignore non-left click, Ctrl/Meta/Shift/Alt, `target != _self`, `download`, external origin, mailto/tel, `rel=external`, `data-no-spa`, fragment-only same-page links.
3. Prevent default and navigate to target URL.
4. Abort previous in-flight navigation with `AbortController`.
5. Request JSON envelope with special header.
6. Validate response status, JSON shape, version, routeID, and layout mode before changing visible DOM.
7. Build new subtree offscreen or prepare mount plan. Determine longest shared layout prefix by stable IDs.
8. Unmount **only** outgoing client components/layouts; preserve shared layout instances.
9. Replace appropriate server-owned page outlet for SSR; for CSR create mount target and mount page with Svelte 5 mount.
10. Set title, URL via `history.pushState`, and page focus. Reset scroll to top unless anchor supplied.
11. `popstate`: fetch the current URL and render without `pushState`; restore stored scroll positions where possible.
12. Network or parsing failure: perform `window.location.assign(destination)` as a dependable fallback; never render half of an old page and pretend navigation succeeded.

### 5.3 Race safety

- Maintain monotonic navigation counter (`navigationId`).
- Each async completion checks that its id is latest before DOM mutation.
- Abort previous fetch, but do not rely on abort alone; stale promise callbacks must be ignored.
- Failed mount triggers hard navigation, logs diagnostic in dev.
- Avoid duplicate global event listeners on HMR.
- Clean up dynamic event handlers and mounted Svelte instances.

### 5.4 Focus and accessibility

- Browser history address updates accurately.
- After navigation set focus to new page heading (`tabindex="-1"`) or main container when appropriate.
- Use `aria-live="polite"` status for navigation changes; avoid announcing duplicate content.
- Preserve expected keyboard link activation and modified-click behavior.
- Respect reduced-motion preferences if transitions are added later.

## 6. Build architecture and production behavior

- Go codegen (templ source => generated Go) and file-route generator occur **before** Go build.
- Vite compiles Svelte 5, client router, page registry, CSS.
- Vite `build.manifest = true` emits hashed asset lookup; backend reads manifest and emits asset URLs, CSS and module preloads. Latest Vite docs support build manifest for backend integration.
- Vite browser module only runs in browser, never within Go.
- Server serves `dist/client` under `/assets` or a single unambiguous prefix. Ensure manifest file itself isn't unintentionally public if not needed.
- Fiber v3 static file middleware API differs from Fiber v2. Use currently documented v3 middleware; do not use removed `app.Static` pattern.
- Production smoke test must work **with Node/npm not installed** on target host after building binaries/assets on build machine. No API calls to dev Vite endpoint in production HTML.
- For Phase 1, `go run ./cmd/example` with built assets is acceptable. Full CLI is Phase 4.

## 7. Task board — tiny, ordered, independently reviewable

Each ticket must touch a narrow set of files and end with commands and proof (test output + route/screenshot where relevant). Don't skip tests to move on.

### A. Bootstrap and toolchain (A01–A08)

- **A01** Create repository, `.gitignore` for `dist`, generated artifacts policy. **Done:** clean `git status`, README names Go 1.26+, Node allowed only build/dev.
- **A02** Initialize Go module. **Done:** `go mod tidy` successful.
- **A03** Install Fiber v3 only, create `cmd/example/main.go` with `GET /healthz => 200 ok`. **Done:** `curl -i` correct; `go test ./...` runs.
- **A04** Add Fiber recovery and request logging middleware. **Done:** panic returns 500 without killing server; logs don't expose request bodies.
- **A05** Initialize frontend package with Vite, Svelte 5, TypeScript. **Done:** `npm run build` passes minimal entry.
- **A06** Add frontend typecheck (`svelte-check`) and minimal `tsconfig`. **Done:** `npm run check` passes.
- **A07** Add templ generator dependency/tool pinned. **Done:** sample templ compiles into Go and produces HTML using Go tests.
- **A08** Add scripts / Makefile for deterministic `generate`, `build`, `test`. **Done:** README commands work on clean checkout.

### B. Routing discovery (B01–B12)

- **B01** Define `RenderMode`, `Route`, `Layout` Go types. **Done:** small constructor tests.
- **B02** Write root page file detection only. **Done:** temp fixture with `app/page.templ` => `/`.
- **B03** Add recursive directory traversal and `about/page.templ`. **Done:** sorted paths stable.
- **B04** Add `.svelte` classification. **Done:** dashboard => CSR mode.
- **B05** Ensure non-page `.svelte`/`.templ` components ignored. **Done:** no accidental routes.
- **B06** Add root `layout.templ` discovery and ancestry resolution. **Done:** both `/` and `/about` have root layout.
- **B07** Add nested `layout.svelte` ancestry mapping. **Done:** dashboard routes report same nested layout ID.
- **B08** Reject duplicate `page.templ`+`page.svelte`. **Done:** actionable test.
- **B09** Reject duplicate layout extensions and mixed unsupported nested layout modes. **Done:** test includes file paths.
- **B10** Normalize route path and distinguish `/` and `/about`. **Done:** path test table.
- **B11** Ensure output deterministic (sort by normalized path + stable tie-break). **Done:** snapshot stable on repeat.
- **B12** Reject/reserve `page.static.templ` with `STATIC_NOT_IMPLEMENTED` rather than silently registering. **Done:** clear error test.

### C. Generated Go routing (C01–C09)

- **C01** Define internal route manifest schema independent of Fiber. **Done:** JSON serialization test and stable IDs.
- **C02** Create code generator writing `generated/routes_gen.go` (initial root route). **Done:** generated file parses with `go/parser`.
- **C03** Add `go/format` to output and deterministic ordering. **Done:** bytes stable across runs.
- **C04** Generated route imports correct templ-generated Go package. **Done:** `go build ./cmd/example` succeeds.
- **C05** Emit Fiber route registrations from descriptors. **Done:** test GET `/` and `/about`.
- **C06** Add CSR route registration mapping `page.svelte` to shell renderer. **Done:** `/dashboard` returns HTML shell.
- **C07** Handle method restrictions (`POST` pages => not SSR output) and 404 fallback. **Done:** tests.
- **C08** Add optional dynamic route syntax `[id]` → Fiber `:id` with collision detection. **Done:** `/products/new` priority test; defer until static paths pass.
- **C09** Generator diagnostics on missing/invalid symbols. **Done:** build error names source file; no runtime panic for missing generated bindings.

### D. templ rendering (D01–D09)

- **D01** Implement `app/layout.templ` minimal full document wrapper. **Done:** outputs `<html>`, metadata, `<body>`.
- **D02** Implement root `app/page.templ` server HTML. **Done:** text present without JS.
- **D03** Implement `app/about/page.templ`. **Done:** text present without JS.
- **D04** Create `renderTemplComponent(ctx, component) ([]byte, error)` using buffer. **Done:** unit test with test component.
- **D05** Fiber adapter passes safe `context.Context` and writes `text/html`. **Done:** tests status/header/body.
- **D06** Generate layout composition root→page for `/`/`/about`. **Done:** navbar appears exactly once.
- **D07** Centralize 404 and 500 HTML. **Done:** no raw stack traces; unique status.
- **D08** Add nested SSR `docs/layout.templ` wrapper. **Done:** composed wrapper order root>docs>page, no duplication.
- **D09** Validate escaped user data (test `<script>` becomes text, not script). **Done:** security test.

### E. Svelte client shell and build assets (E01–E11)

- **E01** Create `dashboard/page.svelte` with counter, no router. **Done:** mounted in standalone browser test fixture.
- **E02** Create `dashboard/settings/page.svelte`. **Done:** separate visible heading.
- **E03** `frontend/entry.ts`: mount CSR component from static import, no routing. **Done:** counter works.
- **E04** Create `frontend/svelte-mounter.ts` wrapping Svelte 5 `mount`/`unmount`. **Done:** lifecycle test.
- **E05** Create manually-coded route import map of `/dashboard` & `/dashboard/settings`. **Done:** dynamic imports load correct component.
- **E06** Generate `route-registry.generated.ts` from same file discovery manifest, mapping **fixed** file paths to import functions. **Done:** stable output; no dynamic arbitrary imports.
- **E07** Vite config with explicit entry, `build.manifest: true`, output `dist/client`. **Done:** output contains manifest and hashed JS.
- **E08** Fiber reads Vite manifest and includes JS/CSS in full HTML. **Done:** production browser loads with Vite dev server off.
- **E09** Add Fiber v3 static asset middleware with safe root. **Done:** asset 200 + MIME, missing asset 404, no traversal.
- **E10** Implement client-route Go HTML shell mount point and metadata. **Done:** reload `/dashboard/settings` boots correct component.
- **E11** Verify Node-free runtime on separate build container/environment. **Done:** binary + dist works without node binary.

### F. Soft-navigation protocol (F01–F12)

- **F01** Document/define TS `NavigationResponse` type matching Go struct. **Done:** checked fixture JSON.
- **F02** `GET /about` + navigation header => JSON with SSR HTML and route metadata. **Done:** Go HTTP integration test.
- **F03** `GET /dashboard` + navigation header => JSON with CSR clientEntry and layout IDs. **Done:** integration test.
- **F04** Ensure normal GET always returns document; special request always has JSON content type. **Done:** test both.
- **F05** Implement same-origin anchor interception and exclusion rules. **Done:** browser tests for modifier/external/download.
- **F06** Implement `fetchNavigation(url, signal)` with Accept and custom header. **Done:** error/redirect test.
- **F07** Implement SSR page outlet replacement with validated envelope. **Done:** `/`→`/about` no reload.
- **F08** Implement CSR page mount from generated registry. **Done:** `/about`→`/dashboard` counter works.
- **F09** Implement CSR unmount on outgoing page and teardown subscriptions. **Done:** mount/unmount instrumentation stable.
- **F10** Add history.pushState + popstate behavior. **Done:** back/forward restores right route.
- **F11** Add abort + stale response navigation IDs. **Done:** artificial delayed `/about` cannot overwrite newer `/dashboard`.
- **F12** Add failure fallback to hard navigation and correct focus/title/scroll. **Done:** offline/500 + accessibility tests.

### G. Nested layout preservation (G01–G10)

- **G01** Formalize layout stack IDs (same root chain => reusable). **Done:** test longest-common-prefix algorithm.
- **G02** Add Svelte `dashboard/layout.svelte` with sidebar and internal counter. **Done:** shows on both dashboard pages.
- **G03** Provide explicit content outlet inside persistent Svelte layout. **Done:** sub-page can mount without replacing layout DOM.
- **G04** Mount dashboard layout once when entering dashboard subtree. **Done:** instrumentation mountCount=1.
- **G05** Switch `/dashboard`→`/dashboard/settings` by replacing child only. **Done:** sidebar counter remains unchanged.
- **G06** Switch back without remount of shared layout. **Done:** layout mountCount still 1.
- **G07** Switch `/dashboard`→`/about`, unmount nested layout. **Done:** destructor called once.
- **G08** Switch `/about`→`/dashboard`, fresh layout state expected. **Done:** new mount count, clean state.
- **G09** Add nested SSR docs layout composition test. **Done:** nested wrappers render in correct order.
- **G10** Reject mixed unsupported SSR-under-CSR nesting at generation time. **Done:** explicit error + test.

### H. QA and release gate (H01–H12)

- **H01** Unit tests: scanner, normalization, validation. **Done:** positive/negative fixtures.
- **H02** Unit tests: generator stable output and compile. **Done:** regenerated source unchanged.
- **H03** HTTP tests: full HTML SSR route `/` and `/about`. **Done:** status, MIME, content.
- **H04** HTTP tests: full CSR shell `/dashboard` and settings. **Done:** scripts/mount present.
- **H05** HTTP tests: nav JSON SSR/CSR and invalid versions. **Done:** schema asserted.
- **H06** Browser test: cross-mode clicks do not reload document. **Done:** `window.__BOOT_COUNT` or performance nav timing proves it.
- **H07** Browser test: back/forward, direct refresh, deep link. **Done:** history correct.
- **H08** Browser test: persisted dashboard layout state. **Done:** state remains between children.
- **H09** Browser test: modified click, external, download, fragment-only. **Done:** native browser behavior.
- **H10** Browser test: slow request races, aborted fetch, server error fallback. **Done:** latest destination wins.
- **H11** Production test: `npm run build`, templ/generator, `go build`, Node-free run, assets 200. **Done:** screenshots/logs retained.
- **H12** Documentation: README runbook, limitations, architecture, troubleshooting. **Done:** newcomer completes demo following docs.

## 8. Checkpoint gates / order

- **Gate 0:** A01–A08; runnable health endpoint, Svelte build, templ compile.
- **Gate 1:** B01–B12; route scanner unit tested without Fiber/Svelte.
- **Gate 2:** C01–C07 + D01–D07; complete `/` and `/about` SSR full reload routes.
- **Gate 3:** E01–E11; `/dashboard` CSR works on direct browser reload using **production compiled assets**.
- **Gate 4:** F01–F12; no-reload cross-mode navigation, accessible and race-safe.
- **Gate 5:** G01–G10; dashboard layout instance remains alive between its children.
- **Gate 6:** H01–H12; green tests and documentation.

Do not start Gate N+1 until Gate N demo passes. Optional dynamic routes C08, nested SSR D08 and SSR under CSR restrictions may be handled at appropriate gates. Avoid parallel edits to generated code.

## 9. Tests and probes

```bash
# Check environment
go version
node --version
npm --version

# Go tests and static analysis
go test ./...
go vet ./...

# Frontend
npm ci
npm run check
npm run build

# Full-document HTTP
curl -si http://localhost:3000/
curl -si http://localhost:3000/about
curl -si http://localhost:3000/dashboard
curl -si http://localhost:3000/dashboard/settings

# Soft nav JSON
curl -si -H 'X-Go-Web-Navigation: 1' -H 'Accept: application/json' http://localhost:3000/about
curl -si -H 'X-Go-Web-Navigation: 1' -H 'Accept: application/json' http://localhost:3000/dashboard

# Browser tests: exact command to be documented when Playwright added
npm run test:e2e
```

Expected: each normal route 200 HTML; navigation request 200 application/json; 404 unknown route; Go server runs without Node after build. No stale response overrides latest navigation. No duplicate Svelte layout mounts across dashboard child transitions.

## 10. Examples of actual implementation contracts (not full runnable app)

### Fiber v3 health endpoint

```go
package main
import (
    "log"
    "github.com/gofiber/fiber/v3"
)
func main() {
    app := fiber.New()
    app.Get("/healthz", func(c fiber.Ctx) error {
        return c.SendString("ok")
    })
    log.Fatal(app.Listen(":3000"))
}
```

### Go templ rendering adapter

```go
func RenderComponent(ctx context.Context, component templ.Component) ([]byte, error) {
    var b bytes.Buffer
    if err := component.Render(ctx, &b); err != nil { return nil, err }
    return b.Bytes(), nil
}
```

### Svelte 5 client mount/unmount sketch

```ts
import { mount, unmount } from 'svelte';
import DashboardPage from '../app/dashboard/page.svelte';
const root = document.querySelector('[data-go-page-root]');
if (!root) throw new Error('Missing page root');
const instance = mount(DashboardPage, { target: root });
// When navigating out: await unmount(instance);
```

### Explicit static dynamic-import registry sketch

```ts
export const pages = {
  'dashboard/page.svelte': () => import('../app/dashboard/page.svelte'),
  'dashboard/settings/page.svelte': () => import('../app/dashboard/settings/page.svelte'),
} as const;
```

### Route response schema

```ts
export type NavigationResponse = {
  version: 1;
  url: string;
  routeId: string;
  mode: 'ssr' | 'csr';
  title: string;
  layouts: string[];
  html: string;
  clientEntry: string | null;
};
```

## 11. Known hazards — read before code

1. **Fiber v3 != Fiber v2**. `fiber.Ctx` interface, changed static middleware. Verify installed v3 docs against pinned module.
2. **templ is compiled Go**. AST scanning route filenames doesn't mean Go can magically call functions exported by generated templates. Generator must follow Go package boundaries and deterministic symbol conventions. Prefer each page package or a generated aggregator whose templ function names are unique; make symbol resolution compile-time, never reflection guesswork.
3. **Svelte 5 mount is not SvelteKit routing**. Vite/Svelte plugin doesn't provide a router automatically. Implement a minimal client router as specified.
4. **Nested Svelte layouts need real persistence**. Repeatedly mounting `layout.svelte` resets its state. Design an outlet ownership model and test persistence.
5. **SSR/CSR boundaries must be intentional**. A templ response is not Svelte hydration, and Svelte client page has no server HTML content. JS-disabled CSR page may show a 'requires JavaScript' message.
6. **Don't update browser URL before commit** unless fallback behavior is correct. On failure, use hard navigation.
7. **Don't assume `fetch` on its own gives SPA**. Preserve history/scroll/focus and handle link modifiers, redirects, error statuses, in-flight races.
8. **Vite hashed assets**. Never hardcode output file hashes; use `.vite/manifest.json`.
9. **Security**. Do not parse untrusted file path as a route. Escape HTML, use safe DOM insertion for trusted generated SSR fragments, and protect static asset file paths from traversal.
10. **CSS**. Svelte-generated CSS can arrive through dynamic JS imports; route-entry CSS must be included in full documents as needed. Test direct refresh without CSS flash.
11. **Multiple nested templ packages**. A templ file's generated Go package must have valid import package name and no cyclic dependencies. Establish `app/` package layout early; don't write one code generator that assumes all routes share a Go package.
12. **Server state**. Vite JS import registry and generated Go manifest must share route IDs. Never independently guess IDs.
13. **No premature feature creep**: auth, RPC, image optimization, SSG, caching not in Phase 1.

## 12. Recommended coding-agent protocol

For each microtask, use this instruction verbatim:

> You are implementing **only task [ID]** from `phase1-implementation-blueprint.md`. Before editing, inspect the current repository and related files. Do not implement future tasks. Preserve the agreed rendering and navigation contracts. Make the smallest coherent change, add one or more focused tests, run the relevant commands, and report: (1) changed files, (2) explanation, (3) tests with actual output, (4) any blockers or assumptions. If a contract is impossible with the current architecture, stop and propose a narrowly scoped fix; do not silently change the spec. Never claim success for unrun tests. Never edit generated files manually.

**First prompt:** Implement **A01** only. Do not scaffold Fiber, Svelte, templ or routing until that task is complete. Then proceed sequentially.

## 13. Primary documentation to consult

- Fiber v3: https://docs.gofiber.io/ and https://docs.gofiber.io/whats_new/
- templ: https://templ.guide/
- Svelte 5: https://svelte.dev/docs/svelte/imperative-component-api
- Vite backend integration: https://vite.dev/guide/backend-integration
- Vite build manifest: https://vite.dev/config/build-options

**Completion definition:** Phase 1 only finishes when compiled Go+Svelte production assets work without Node at runtime, direct refresh to both SSR and CSR routes works, soft navigation survives history/races/errors, and dashboard nested layout state persists between child routes.
