# Add CSS

## Global styles

Create a stylesheet, for example `frontend/app.css`:

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

Add this import near the top of `frontend/entry.ts`:

```ts
import './app.css';
```

Run `meta dev` to develop, or `meta build` to produce the client assets. Vite includes the CSS in its build and Meta loads the entry's stylesheet on pages served with the configured assets. If you use the public runtime directly, configure `ClientDir` for production or `DevOrigin` behind the Meta development proxy.

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
