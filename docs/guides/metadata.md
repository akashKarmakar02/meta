# Page titles and metadata

Templ pages and templ layouts can set document titles, descriptions, canonical URLs, Open Graph tags, and X/Twitter cards with `metadata.Set`. Meta renders those values into the initial HTML `<head>` and updates them during soft navigation.

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

## Canonical and social metadata

Set the canonical URL and sharing fields in the same templ component:

```templ
{{ metadata.Set(ctx, metadata.Page{
    Title: "About us",
    Description: "Meet our team.",
    Canonical: "https://example.com/about",
    OpenGraph: metadata.OpenGraph{
        Title: "About us",
        Description: "Meet the team.",
        Image: "https://example.com/images/team.jpg",
        URL: "https://example.com/about",
        Type: "website",
        SiteName: "Example",
    },
    Twitter: metadata.Twitter{
        Card: "summary_large_image",
        Title: "About us",
        Description: "Meet the team.",
        Image: "https://example.com/images/team.jpg",
        Site: "@example",
    },
}) }}
```

Each nested field inherits independently from the nearest templ layout when omitted. Meta does not automatically copy the page title or description into social fields, or derive a canonical URL. Set a canonical URL for each page rather than inheriting the same URL for unrelated pages. For social previews, use publicly reachable absolute HTTPS URLs: a crawler may not resolve root-relative image URLs as you expect.

URL fields accept absolute HTTP(S) URLs or root-relative paths. Meta omits values with other schemes, network-path references such as `//example.com`, credentials, or invalid syntax. Values are escaped in HTML. During soft navigation Meta updates and removes only the head elements it owns. It does not edit unrelated tags.

The repository's [`app/posts/[postId]/page.templ`](../../app/posts/%5BpostId%5D/page.templ) demonstrates loader-backed canonical and sharing metadata. Its relative URLs are suitable for local testing; use absolute URLs for actual sharing.
