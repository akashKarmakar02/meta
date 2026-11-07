# Build and deploy

Run these commands from the project root:

```sh
meta setup
meta build
./bin/app
```

`meta build` generates templ and route code, builds the Vite assets into `dist/client`, and compiles the server to `bin/app`. The generated server uses `PORT` when set and otherwise listens on port 3000. Run it with the project root as the working directory, or adapt `ClientDir` in `cmd/app/main.go` to point to the deployed assets.

Deploy the Go binary, its matching `dist/client` directory, and the `public/` directory together. The server reads public files directly from `public/`. The production server does not need Node.js, npm, Vite, or Air. Your build environment needs Go, Node/npm, and access to the project dependencies. Development additionally needs Air.

The generated production server loads `.env` and `.env.production` at startup, while shell variables take precedence. Browser `VITE_` values are embedded at build time. See [environment variables](environment.md).

For action endpoints, configure the external trusted origin. For forms, also configure a persistent CSRF secret shared by all instances. Keep credentials out of source control and use HTTPS in production. These options are application configuration; the starter does not create a production secret or authentication system. See [forms](forms.md).

The starter server creates a Fiber app with `server.New`. Configure application-owned loaders and actions in `cmd/app/main.go` before starting the server. `meta build` does not run database migrations, set up TLS, provision a reverse proxy, or manage process supervision.
