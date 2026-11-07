# Public files

Put files that anyone may download in your project's `public/` directory. Meta serves them from the site root. For example:

```text
public/robots.txt         → /robots.txt
public/favicon.ico        → /favicon.ico
public/images/logo.svg    → /images/logo.svg
public/files/guide.pdf    → /files/guide.pdf
```

Reference a file with its root-relative URL:

```templ
<img src="/images/logo.svg" alt="Company name"/>
<a href="/files/guide.pdf" download>Download the guide</a>
```

The starter server reads files directly from `public/` in both development and production. After editing a file, refresh the browser. Deploy the `public/` directory alongside the Go binary and `dist/client`. The starter disables Vite's automatic public-directory copy so Vite does not follow symlinks when building.

Public files use `Cache-Control: no-cache`, so browsers must revalidate them. Their names are not content-hashed. Do not put secrets, source files, private uploads, or other restricted data in `public/`.

Only regular files are served. Directory listings and hidden paths are not exposed, except files under `.well-known/`. Symlinks are not served, and file access is confined to the public directory. Missing files fall through to application routes; if a file and a page have the same URL, the file takes precedence. Reserve `/_meta`, `/_go_meta_vite`, `/assets`, and `/healthz` for the framework. Public files cannot replace those paths.

## Configure an existing application

`server.Config.PublicDir` selects the directory; leaving it empty disables public file serving. In an application created before this feature, set it alongside the existing asset configuration in `cmd/app/main.go`:

```go
cfg.PublicDir = "public"
```

Set `publicDir: false` in `vite.config.ts` to stop Vite from copying these files separately. Vite's copy follows symlinks, so inspect any existing build output before deploying it. An absent public directory does not prevent startup, so you can create it later.

The example application includes `public/meta-example.txt` and `public/robots.txt`. With `meta dev` running from the repository root, open <http://localhost:3000/meta-example.txt> or <http://localhost:3000/robots.txt> to try them.
