# Pages and layouts

Meta discovers routes under `app/`. The filename determines how a page renders:

- `page.templ` is rendered on the server with templ.
- `page.svelte` is rendered in the browser with Svelte 5. It is not server-rendered.

For example:

```text
app/page.templ                  /
app/about/page.templ            /about
app/dashboard/page.svelte       /dashboard
app/users/[id]/page.templ       /users/:id
app/docs/[...slug]/page.templ   /docs/*
```

A directory cannot contain both page filenames. Static routes take precedence over parameter and catch-all routes. A catch-all must be the last path segment and matches one or more segments. Do not use a dynamic route directory for general Go code. Dynamic templ directories are staged into generated packages, so put Go helpers in an ordinary Go package and import them.

## Add a server-rendered page

Create `app/about/page.templ`:

```templ
package about

templ About() {
    <main>
        <h1>About</h1>
        <p>This page renders on the server.</p>
    </main>
}
```

Run `meta generate` if you are not using `meta dev`. Visit `/about`. Static templ page component names are formed by joining the directory segments and capitalizing the first letter of each segment. For example, `app/about/page.templ` uses `About` and `app/docs/intro/page.templ` uses `DocsIntro`. The root page and pages in dynamic directories use `Page`. Static templ layouts use the same directory-derived name followed by `Layout`; the root layout and dynamic layouts use `Layout`. Choose static directory names that form valid Go identifiers.

## Read a route parameter

For `app/users/[id]/page.templ`, use the request context:

```templ
package users

import "github.com/akashKarmakar02/meta/routeparams"

templ Page() {
    {{ id, _ := routeparams.FromContext(ctx).Get("id") }}
    <main>
        <h1>User</h1>
        <p>User ID: { id }</p>
    </main>
}
```

`Get` returns a string and a boolean. Use typed methods such as `Int64` when the value must be numeric, and handle missing or invalid input in a real application. A catch-all value is a slash-joined string. For data access and proper error responses, use a [loader](loaders.md).

## Add a Svelte page

Create `app/counter/page.svelte`:

```svelte
<script lang="ts">
  let count = $state(0);
</script>

<main>
  <h1>Counter</h1>
  <button onclick={() => count++}>Count: {count}</button>
</main>
```

## Layouts

Add `layout.templ` or `layout.svelte` to a route directory to apply a visual layout. Meta selects the nearest layout in the page's directory or an ancestor directory. It does not compose every ancestor layout. A templ layout can wrap templ or Svelte pages. A Svelte layout can wrap Svelte pages only. Do not put both layout types in the same directory.

The framework creates the surrounding HTML document. A templ layout accepts a `templ.Component` and renders it with `@content`. It should not create its own `<html>`, `<head>`, or `<body>` elements. See [page titles and metadata](metadata.md) for current document limitations.

### Svelte layout outlets

A Svelte layout must identify itself and contain exactly one initially empty outlet where Meta mounts the page. For `app/dashboard/layout.svelte`:

```svelte
<div data-go-layout="app/dashboard/layout.svelte">
  <nav><a href="/dashboard">Dashboard</a></nav>
  <div data-go-layout-outlet></div>
</div>
```

The `data-go-layout` value must match the layout's file path from the project root. Change it when you move or rename the layout. Meta mounts each page separately inside the outlet; the outlet is not a Svelte `children` snippet. To share a Svelte provider with pages, replace the plain outlet with `LayoutOutlet` inside the provider:

```svelte
<script lang="ts">
  import LayoutOutlet from "../../frontend/LayoutOutlet.svelte";
  import { ClerkProvider } from "svelte-clerk/client";

  const publishableKey = import.meta.env.VITE_CLERK_PUBLISHABLE_KEY;
</script>

<main data-go-layout="app/dashboard/layout.svelte">
  <ClerkProvider {publishableKey}>
    <LayoutOutlet />
  </ClerkProvider>
</main>
```

Projects created before v0.1.0-beta.2 have an older copied frontend runtime: updating the Go module or CLI alone does not update those files. Merge the v0.1.0-beta.2 runtime changes into your project before using `LayoutOutlet`; preserve any application edits in `frontend/`. The affected files are `frontend/LayoutOutlet.svelte`, `layout-context.ts`, `svelte-mounter.ts`, `layout-mount.ts`, `layout-child-swap.ts`, `navigation-pipeline.ts`, and `entry.ts`.

Adjust the relative import for the layout location. `LayoutOutlet` carries the Svelte context at its position to the separately mounted page. The original plain outlet remains supported but does not share context. See [Clerk authentication](clerk.md).

## Public SSR page caching (beta.6)

SSR routes may opt into eager document/navigation caching through
`pagecache.PublicRoutes("blog/[slug]/page.templ")` on a configured
`server.Config.PageCache`. IDs are generated source filenames; dynamic params
and exact query URLs remain separate entries. CSR/Svelte routes are excluded.
The first HTML or soft-navigation request generates both responses from one
loader/page/layout snapshot, before returning. Later requests select the
correct representation without rerendering or reloading data.

Required middleware, route authorization, and CSRF execute before lookup.
Cookies, authorization, private/no-store, Set-Cookie, nonce and Vary responses
bypass shared caching. Only explicitly reviewed anonymous deterministic pages
belong in the public list. After mutations, `pc.InvalidatePath(ctx, concretePath)`
invalidates both representations and every query variant, including across
sequential disk-backed restarts. See [Caching](caching.md) for deployment IDs,
security rules, format migration, concurrency, and persistent invalidation.
