# Page titles and metadata

Templ pages and templ layouts can set the document title and description with `metadata.Set`. Meta renders those values into the initial HTML `<head>` and updates them during soft navigation.

Set application defaults in the root `app/layout.templ`:

```templ
package app

import "github.com/akashKarmakar02/meta/metadata"

templ Layout(content templ.Component) {
    {{ metadata.Set(ctx, metadata.Page{
        Title: "My site",
        Description: "Updates from my site.",
    }) }}
    <div>
        @content
    </div>
}
```

The project created by `meta init` includes a metadata example in its root layout. Edit it to suit your application. The repository example also sets defaults in [`app/layout.templ`](../../app/layout.templ), overrides them in [`app/about/page.templ`](../../app/about/page.templ), and uses loader data in [`app/posts/[postId]/page.templ`](../../app/posts/%5BpostId%5D/page.templ).

## Override a page

Call `metadata.Set` in the page's templ component:

```templ
package about

import "github.com/akashKarmakar02/meta/metadata"

templ About() {
    {{ metadata.Set(ctx, metadata.Page{
        Title: "About us",
        Description: "Meet our team.",
    }) }}
    <main>
        <h1>About</h1>
    </main>
}
```

The page's nonempty fields override the same fields in its nearest templ visual layout. If a page omits a field, it inherits that field from the layout. Meta uses only the nearest visual layout, not every ancestor layout. Empty strings mean “inherit”; they do not explicitly remove a layout value. If neither sets a title, Meta retains its existing default title behavior. Each render has its own metadata, and values are escaped in HTML.

## Use loader data

Read the same loader result used by the page. Replace the module path and `posts.Post` with your application's types:

```templ
package blog

import "example.com/my-app/app/posts"
import "github.com/akashKarmakar02/meta/loaderdata"
import "github.com/akashKarmakar02/meta/metadata"

templ Page() {
    {{
        post, ok := loaderdata.Get[posts.Post](ctx)
        if ok {
            metadata.Set(ctx, metadata.Page{
                Title: post.Title,
                Description: post.Excerpt,
            })
        }
    }}
    <main>
        if ok {
            <h1>{ post.Title }</h1>
            <p>{ post.Content }</p>
        }
    </main>
}
```

Register the loader for the route as described in [loaders](loaders.md). Meta renders the page once and does not run the loader a second time for metadata.

## Svelte pages

Svelte pages run in the browser. A nearest templ layout can supply their initial title and description, but Meta cannot extract server-rendered metadata from a Svelte component. Svelte's `<svelte:head>` can add client-side tags after JavaScript runs. Meta's current navigation runtime also updates titles, so check title behavior when combining them. Client-side tags are not reliable for crawlers that do not run JavaScript.

Only `Title` and `Description` are supported by this API. Canonical URLs and Open Graph tags are not yet available through `metadata.Page`.
