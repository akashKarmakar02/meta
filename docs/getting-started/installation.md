# Install Meta and create a project

You need Go 1.26.10 or newer, Node.js and npm, and [Air](https://github.com/air-verse/air) on your `PATH`. Go may download the required Go toolchain automatically if `GOTOOLCHAIN` permits it. Node is needed to develop and build the frontend, not to run the production server.

Install the CLI and Air:

```sh
go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.2
go install github.com/air-verse/air@latest
```

If your shell cannot find either command, add your Go binary directory to `PATH`. With the default `GOBIN`, Bash users can run:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
hash -r
```

If `go env GOBIN` prints a directory, add that directory instead. Add the appropriate export to your shell configuration to keep it in new terminals. Check the CLI with `meta --version`.

Create a project in a directory that does not already exist:

```sh
meta init my-app --module example.com/my-app
cd my-app
meta setup
meta dev
```

Open <http://localhost:3000>. The home page is rendered by templ. The dashboard is rendered by Svelte in the browser. Edit `app/page.templ` and save it to see the development server rebuild. Edit `app/dashboard/page.svelte` to try Svelte hot module replacement.

`meta init` writes files but does not install dependencies. If you omit `--module`, the module defaults to `example.com/<directory-name>`. Use your own module path before publishing the application. `meta setup` resolves the pinned templ tool and installs npm dependencies. `meta dev` runs Vite and Air until you press Ctrl+C.

Build the application when you are ready to run it without the development server:

```sh
meta build
./bin/app
```

Run the binary from the project root so it can find `dist/client`. See [build and deploy](../guides/deployment.md) for configuration and production requirements.
