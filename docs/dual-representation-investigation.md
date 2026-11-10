# SSR cache investigation (beta.5 → beta.6)

Baseline: remote `main`, `c88054ae03508a83b8c26fe6370d4f1296f88ad5`.
The working guide references `phase1-implementation-blueprint.md`, but that
file is absent from this checkout and its Git tree. The supplied architecture
constraints remain binding.

## Existing pipeline, before changes

`server.New` installs recovery, observability, path validation, application
middleware, route-context binding, route policies, and optional form-CSRF
middleware. It then invokes `WithLoader(PageCache.Wrap(renderer))` for SSR.
Policies therefore precede both loader and response cache. Policies mark their
responses `private, no-store`; CSRF can issue a request-specific cookie/token.

`WithLoader` executes a registered typed loader against immutable route params
and query values. SSR data is attached to the Go context through `loaderdata`.
Redirects and errors terminate dispatch before rendering. CSR data instead
crosses a JSON boundary; Svelte itself is never rendered on the server.

`SSRNavigationHandler` selects JSON only for `X-Go-Web-Navigation: 1`.
The document path resolves `SSRPage` (the page wrapped in its selected layout),
renders it with a metadata collector, and constructs the document and asset
tags. Generated code marks the page metadata so it overrides layout metadata.
The navigation path resolves `BarePage`, renders it once, passes its bytes to
`VisualLayout`, renders that layout, and merges layout/page metadata. It builds
the existing version-1 envelope with concrete requested URL, route ID, mode,
layout IDs, heading-derived title, visual HTML, and structured metadata.
`SendNavigation` returns `application/json` and `Cache-Control: no-store`.
The selected layout follows nearest-layout ownership (at most one in generated
routes), rather than stacking all ancestor templ layouts.

`PageCache.Wrap` stores content type + NUL + body under the `page` namespace,
keyed by pattern/params/decoded query. It does not distinguish navigation from
documents, inspect sensitive requests/responses, coalesce misses, or fence
invalidations. Consequently the first representation poisons the other.
The loader also runs on hits. Status and request-specific headers are not
persisted. `InvalidatePath` relies on a process-local path/key index, lost on
restart. The disk store itself uses atomic rename and CRC-checked files at
deterministic hashed paths; its opaque byte entries survive restart.

Existing tests cover basic HTML hits/query variants/invalidation, SSR/CSR
metadata, single page rendering during navigation, and real-browser cross-mode,
history, layout ownership, and stale-navigation behavior. They do not combine
full-response caching with both request types or restart invalidation.

## Implementation decision

Provide a deliberate pair generator, called only on eligible cache misses.
Render the selected visual layout once around a capture component. When it
invokes its child, render and capture the bare page once using layout-provided
context and page metadata precedence. Build HTML and navigation JSON from those
same visual bytes and the shared metadata collector. No second HTTP
request, fabricated Fiber context, document parsing, or repeated templ tree.
Keep uncached/development paths unchanged. Put the lookup outside loader
dispatch but inside existing policies and CSRF middleware.

Store one versioned, checksummed pair. Persist a random per-path generation
token under a deterministic metadata key in the same store, with no expiry.
Include the token in every page/query key. Invalidation replaces the token,
invalidating every variant without scanning disk. A per-cache lock orders token
checks/writes against invalidation. A shared flight per pair identity coalesces
HTML and JSON misses. Generation stays synchronous on the owning Fiber request,
with a bounded context; no Fiber object outlives its handler.

Deployment model: one PageCache coordinator per store, one active process per
disk directory; sequential restarts supported. Distributed invalidation is not
provided by the Store contract. Old unreachable generations can be reclaimed
by clearing an offline cache directory; TTL expires page entries normally.
