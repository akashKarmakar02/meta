# Project structure

A project created by `meta init` starts with these paths:

```text
my-app/
  app/
    layout.templ              # root visual layout
    page.templ                # home page, /
    dashboard/page.svelte     # browser-rendered /dashboard
  cmd/app/main.go             # server configuration and entry point
  frontend/                   # copied browser runtime
  public/robots.txt           # public file served at /robots.txt
  go.mod                      # Go module and templ tool version
  package.json                # frontend dependencies and build script
  vite.config.ts              # Vite configuration
  .air.toml                   # Go development watcher configuration
  .gitignore
```

`meta setup`, `meta generate`, `meta dev`, and `meta build` create additional files and directories:

```text
  generated/                         # generated Go routes and dynamic templ packages
  frontend/route-registry.generated.ts
  app/**/*_templ.go                  # templ-generated Go
  node_modules/                      # npm dependencies
  dist/client/                       # production frontend assets
  bin/app                            # production server binary
  .tmp/                              # development and build intermediates
  go.sum and package-lock.json       # dependency checksums and npm lockfile
```

The starter's `.gitignore` excludes generated source, dependencies, and build output. Commit source files and the dependency lockfiles your team uses. Generated output can be recreated with `meta generate` and `meta build`.

In TypeScript and Svelte files, `@/` imports from the project root — for example `import { greeting } from '@/lib/greeting'` resolves `lib/greeting.ts` next to `package.json`. The mapping lives in `tsconfig.json` (`paths`) for editors and type checks, and in `vite.config.ts` (`resolve.alias`) for `meta build` bundles. Relative imports keep working; use whichever reads better.

## What to edit

Edit `app/` pages, layouts, components, and your own Go packages. Put publicly accessible files in `public/`. Add loader and action bindings to `cmd/app/main.go` or import them from your own packages. You can change `go.mod`, `package.json`, `vite.config.ts`, `.air.toml`, and `.gitignore` for your application. Keep the Vite manifest, entry point, output directory, and Air build steps consistent with the server and CLI.

`frontend/` is copied into each new project. It includes Meta's browser navigation and Svelte mounting code. You can add a global stylesheet import to `frontend/entry.ts`, but treat the runtime files as framework code. Changes there can affect navigation, and updating the Go module does not automatically update the copied runtime. Review runtime changes when upgrading Meta.

## What not to edit

Do not edit `generated/`, `frontend/route-registry.generated.ts`, or `*_templ.go` by hand. A generation step can overwrite them. Edit the corresponding page, layout, or component source and run `meta generate`. Do not edit files in `node_modules/`, `dist/`, `.tmp/`, or `bin/` as application source.

A file becomes a route only when its name and location match Meta's routing conventions. A normal component file does not become a page. See [pages and layouts](../guides/routing.md).
