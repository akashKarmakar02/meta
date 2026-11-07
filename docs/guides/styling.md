# Add CSS

Projects created with `meta init` include Tailwind CSS. Use utility classes in templ and Svelte files:

```templ
<h1 class="text-3xl font-bold">Hello, Meta</h1>
```

Add global CSS or customize Tailwind in `frontend/app.css`. The starter imports this file through `frontend/entry.ts`, and the Vite plugin builds the stylesheet. In production, Meta includes the generated stylesheet in the HTML; Node is only needed to build it. During development Vite delivers CSS updates through its JavaScript client.

Tailwind scans your source for complete class names. Write `text-red-600` or `text-blue-600` in full rather than assembling a class such as `text-` plus a color at runtime. See [Tailwind's source detection guide](https://tailwindcss.com/docs/detecting-classes-in-source-files).

## Plain CSS from a templ layout

Create a stylesheet in `public/app.css`:

```css
body {
  margin: 0;
  font-family: system-ui, sans-serif;
  color: #17202a;
  background: #f8fafc;
}

main {
  max-width: 72rem;
  margin: 0 auto;
  padding: 2rem;
}
```

Link to it from a templ layout, such as `app/layout.templ`:

```templ
package app

templ Layout(content templ.Component) {
    <link rel="stylesheet" href="/app.css"/>
    <div>
        @content
    </div>
}
```

Meta serves `public/app.css` at `/app.css` in development and production. The browser loads it without JavaScript. After editing the file, refresh the browser to see your changes. Deploy `public/` alongside your production binary; see [public files](public-files.md) for configuration.

Meta uses the nearest visual layout for each page. If a page uses a different layout, link the stylesheet from that layout too. Stylesheet rules are global, so use class names to avoid unintended changes to other pages.

## Vite-managed styles in existing projects

The Tailwind starter already includes this setup. In an existing project, install `tailwindcss` and `@tailwindcss/vite`, add `tailwindcss()` to the plugins in `vite.config.ts`, and create `frontend/app.css`:

```css
@import "tailwindcss";
@source "../app";
```

Then add this import near the top of `frontend/entry.ts`:

```ts
import './app.css';
```

Run `meta dev` to develop, or `meta build` to produce the client assets. Vite includes the CSS in its build, and Meta adds a stylesheet link for the hashed production asset. In development, Vite delivers updates through its JavaScript client. If you use the public runtime directly, configure `ClientDir` for production or `DevOrigin` behind the Meta development proxy.

`frontend/entry.ts` is part of the copied Meta browser runtime. Keep its existing imports and boot logic. When upgrading, review changes to the starter runtime before merging your CSS import into a newer entry file.

## Svelte component styles

Svelte components can define styles alongside their markup:

```svelte
<h1 class="heading">Dashboard</h1>

<style>
  .heading {
    color: rebeccapurple;
  }
</style>
```

Svelte scopes these styles to the component. Use a global stylesheet for styles shared with templ pages. A templ page can use normal `class` attributes; its visual layout is rendered on the server.
