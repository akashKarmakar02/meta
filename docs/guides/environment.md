# Environment variables

Meta uses environment variables for configuration. Keep secrets on the server. Treat every value whose name starts with `VITE_` as public: Vite includes it in browser JavaScript.

## Development

Create a `.env` file at the project root:

```dotenv
APP_MESSAGE=Hello from the Go server
VITE_PUBLIC_MESSAGE=Hello from the browser
```

The framework repository includes example pages at `/envexample` and `/envbrowser`. These examples display their values, so do not put secrets in them. New projects do not include these pages.

For development, the shell has the highest priority, followed by `.env.local`, then `.env`. Files are optional. If a shell variable is present but empty, it still wins. Restart `meta dev` after changing either file.

`PORT` and `GO_META_VITE_PORT` can be set in these files or in the shell. Meta keeps its internal `GO_META_DEV`, `GO_META_PUBLIC_PORT`, `GO_META_VITE_ORIGIN`, and `GO_META_DEV_SOCKET` keys reserved for the development supervisor. Ordinary application variables, such as `GO_META_FORM_SECRET`, can be placed in `.env.local`.

## Production

`meta build` uses `.env.production` over `.env` for public `VITE_` values. Shell variables take priority over both. The values are embedded in the client bundle when it is built; changing an env file later requires another `meta build` to update the browser bundle.

The generated Go application's production binary reads `.env.production` over `.env` at startup. The shell again takes priority. Run the binary from the project root, or change the directory passed to `env.LoadProduction` in `cmd/app/main.go`. Restart the binary after changing server values. `.env.local` is not loaded in production.

An application created before this feature can opt into production loading by calling `env.LoadProduction` once at startup, before reading configuration or starting goroutines:

```go
import (
    "log"
    "os"
    "github.com/akashKarmakar02/meta/env"
)

func main() {
    if os.Getenv("GO_META_DEV") != "1" {
        if err := env.LoadProduction("."); err != nil {
            log.Fatal(err)
        }
    }
    // Configure and start the server.
}
```

`meta dev` supplies `GO_META_DEV=1` to the backend so it does not load production files. The generated Vite configuration sets `envDir: false`, allowing Meta to select the exact files described above. Add that setting to an existing `vite.config.ts` when upgrading; otherwise Vite may load its own additional env files. Vite commands invoked outside the Meta CLI need their own environment setup.

## File syntax

Use one `NAME=value` assignment per line, optionally prefixed with `export`. Empty values, comments, and single- or double-quoted values are supported. Quote values containing `#`. Double-quoted values support Go string escapes such as `\n`; single-quoted values are literal. Meta does not expand `$NAME` references or execute shell commands. Invalid assignments stop the command or server and identify the file and line without printing the value.

## Server values and secrets

Read server values in Go with `os.Getenv` or `os.LookupEnv`:

```go
apiKey := os.Getenv("PAYMENT_API_KEY")
```

A non-`VITE_` variable is not automatically sent to the browser. Your application can still leak it by rendering it in HTML, returning it from an API or a Svelte loader, logging it, or putting it in a public file. Only send data the user is allowed to see.

For deployed secrets, prefer the process environment or a secret manager. The starter ignores `.env` and `.env.*` files in Git. Do not commit credentials. New projects do not include an env file; create the files you need.

## Browser values

Use the `VITE_` prefix only for non-secret configuration:

```svelte
<script lang="ts">
  const message = import.meta.env.VITE_PUBLIC_MESSAGE;
</script>

<p>{message}</p>
```

Anyone can read these values in browser-delivered JavaScript. Meta passes only `VITE_` values from env files to Vite, although Vite inherits the shell environment and runs project build plugins. Never put a secret under a `VITE_` name. Meta's env loader does not expand variable references. See [Vite environment variables](https://vite.dev/guide/env-and-mode) for the browser-side API.
