# Add CSS

Projects created with `meta init` include Tailwind CSS. Use utility classes in templ and Svelte files:

```templ
<h1 class="text-3xl font-bold">Hello, Meta</h1>
```

Add global CSS or customize Tailwind in `frontend/app.css`. The starter configures this file as a separate Vite entry. Meta puts a stylesheet link in the initial HTML in both development and production, so templ pages receive their styles without waiting for JavaScript. In development, Vite processes the stylesheet and delivers live updates when JavaScript is enabled; with JavaScript disabled, refresh after editing. Production uses a hashed CSS file and needs Node only during the build.

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

Add both entries to `vite.config.ts` under `build`:

```ts
rollupOptions: { input: ['frontend/entry.ts', 'frontend/app.css'] },
```

Set `GlobalStylesheet: true` in `server.Config` in `cmd/app/main.go`. Remove `import './app.css'` from `frontend/entry.ts` if present so the stylesheet isn't loaded twice. Run `meta dev` to develop, or `meta build` to produce the client assets. If you use the public runtime directly, configure `ClientDir` for production or `DevOrigin` behind the Meta development proxy.

Projects that keep the existing JavaScript CSS import and leave `GlobalStylesheet` unset continue to work as before. `frontend/entry.ts` is part of the copied Meta browser runtime; keep its other imports and boot logic intact.

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
