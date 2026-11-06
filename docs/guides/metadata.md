# Page titles and metadata

Meta currently owns the HTML document. There is no public per-page metadata API for setting the initial `<title>`, description, canonical URL, or Open Graph tags in the server-rendered `<head>`. Adding those tags inside a page or visual layout puts them in the document body, so it is not a reliable metadata solution.

On a direct request, the current SSR document title is `go-meta`. A direct Svelte page request uses its route ID as the title. During soft navigation to a templ page, Meta derives the title from the first `<h1>` in the rendered page. During soft navigation to a Svelte page, it derives the title from the last path segment. Keep the first heading plain text, since the current SSR title extraction reads HTML between the opening and closing heading tags.

For example, this heading supplies `About our team` as the soft-navigation title for a templ page:

```templ
templ Page() {
    <main>
        <h1>About our team</h1>
        <p>Meet the team.</p>
    </main>
}
```

This does not change the title in the initial HTML response. Client-side code can assign `document.title`, but that requires JavaScript and Meta may replace it during navigation. It also cannot supply server-rendered metadata to crawlers. Applications that need reliable page-specific head tags must provide that capability outside the current public Meta runtime or wait for a public metadata API. Avoid editing the framework's `internal/render` package from an application: it is not an importable extension point.
