# Docker images

Meta's production server is a single Go binary serving static assets from `dist/client` and `public/`. A Docker image needs exactly three things at runtime: the binary, those two directories, and the project root as the working directory. Node.js, npm, Vite, templ, and Air are **build-time only** — never ship them in the final image.

This guide shows a multi-stage build that compiles in one stage and copies only the production artifacts into a minimal runtime image.

## Project layout recap

A built Meta project looks like this:

```text
my-app/
  bin/app          # production server binary (from meta build)
  dist/client/     # hashed Vite assets (entry, page chunks, CSS)
  public/          # public files served from /robots.txt etc.
  cmd/app/main.go  # server configuration
  app/             # pages, layouts, components
  frontend/        # browser runtime sources
  generated/       # generated Go routes (build input)
```

The server expects its working directory to be the project root so relative paths (`public`, `dist/client`) resolve. You can override them with `PublicDir` and `ClientDir` in `cmd/app/main.go`.

## Multi-stage Dockerfile

Create this as `Dockerfile` in your project root:

```dockerfile
# syntax=docker/dockerfile:1

########## Build stage: Go + Node toolchain ##########
FROM golang:1.26 AS build

# Node.js for the Vite asset build.
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Cache Go dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Cache npm dependencies first.
COPY package.json package-lock.json ./
RUN npm ci

# Now the source.
COPY . .

# meta build runs templ generate (via go.mod's tool directive), routegen,
# vite build, and go build. Only the CLI install is needed. Pin it for
# reproducible images.
RUN go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.4 \
    && meta build

########## Runtime stage: Go-only, no Node ##########
FROM debian:bookworm-slim AS runtime

# libwebp/libavif are only needed if you build with -tags imageproc_native
# (see "Image optimization in Docker" below); omit them for the default build.
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# curl for the container healthcheck (bookworm-slim ships no wget).
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Run as an unprivileged user.
RUN useradd --system --uid 1001 --create-home meta
WORKDIR /app

# Only the artifacts the server needs at runtime.
COPY --from=build /src/bin/app ./bin/app
COPY --from=build /src/dist/client ./dist/client
COPY --from=build /src/public ./public

USER meta
EXPOSE 3000
ENV PORT=3000

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD ["curl", "-fsS", "http://127.0.0.1:3000/healthz"]

CMD ["./bin/app"]
```

Notes on the choices:

- **`debian:bookworm-slim` over `scratch`/`alpine`:** the binary is dynamically linked against glibc (and CGO if you use SQLite or similar), so scratch needs special handling. Alpine's musl can break CGO binaries. If your binary is pure Go (`CGO_ENABLED=0`, no SQLite), swap the runtime stage for `FROM scratch`, add `USER 1001:1001` and copy `/etc/ssl/certs/ca-certificates.crt` if you make outbound HTTPS calls.
- **`meta build` inside the container:** it requires network access to fetch the `meta` CLI and templ unless you vendor them. Pin versions for reproducible images.
- **Health check:** the scaffolded server serves `/healthz` when `HealthCheck: true` in `server.Config`. Keep `HealthCheck: true` or replace the probe.

If you prefer building outside Docker (CI already runs `meta build`), the runtime stage is enough — copy `bin/app`, `dist/client`, and `public/` from the checkout instead of the build stage, and the base image only needs to run a static binary.

## Image optimization in Docker

**Short answer: yes — the image works without any image-processing packages, by design.** Meta's native WebP/AVIF processor is behind the `imageproc_native` build tag. A plain `meta build` compiles a pure-Go binary with zero image-library dependencies, and the framework fails closed without them: the scaffolded app never registers an image endpoint at all, and apps modeled on `cmd/example` serve a truthful 501 "backend unavailable" error from `/_meta/image` instead of fabricating unoptimized bytes. Pages, assets, forms, and actions are unaffected.

If you want real optimization, two things must be true: your app must contain build-tagged wiring for the native processor (see `cmd/example/imageproc_native.go`, the contract is the `imageopt` package), and the binary must be compiled with `-tags imageproc_native`. In Docker:

```dockerfile
########## Build stage additions ##########
# Native image processing needs the WebP and AVIF C libraries at compile time.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libwebp-dev libavif-dev pkg-config \
    && rm -rf /var/lib/apt/lists/*

# meta build passes its environment through to go build, so the tag rides
# along on GOFLAGS:
ENV GOFLAGS="-tags=imageproc_native"
RUN go install github.com/akashKarmakar02/meta/cmd/meta@v0.1.0-beta.4 \
    && meta build

########## Runtime stage additions ##########
# Only the runtime libraries, not the -dev headers. Package names are for
# Debian bookworm; check your base's naming.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libwebp7 libavif15 \
    && rm -rf /var/lib/apt/lists/*
```

Caveats:

- Verify the tag actually took effect after building: `docker run --rm my-app sh -c "ldd bin/app | grep webp"` should show `libwebp.so`. An untagged build succeeds silently — that failure mode is why this check is worth keeping in CI.
- The resulting binary is dynamically linked against those libraries, so the runtime image cannot be `FROM scratch` — keep the Debian slim base (or copy the `.so` files yourself).


## .dockerignore

Keep build intermediates out of the image and cache-busting small:

```text
.git
node_modules
dist
bin
.tmp
generated
frontend/route-registry.generated.ts
*.md
.env
.env.*
!.env.example
```

`meta build` regenerates `generated/` and `dist/` inside the image, so there is no need to copy stale local copies.

## Environment variables

The production server loads `.env` and `.env.production` at startup, with shell variables taking precedence. In containers, pass configuration as environment variables instead of baking `.env` files into the image (`TrustedOrigin`, CORS origins, and other `server.Config` values are set in `cmd/app/main.go` before the server starts; read them from `os.Getenv` there if you want them controllable per deployment):

```sh
docker run \
  -e PORT=3000 \
  -e FORM_CSRF_SECRET=... \
  -p 3000:3000 \
  my-app:latest
```

See [environment variables](environment.md) for the full list, and [forms](forms.md) for the CSRF secret requirement when running multiple instances.

## Building and running

```sh
docker build -t my-app .
docker run -p 3000:3000 my-app
curl -i http://localhost:3000/healthz
```

The production container runs no Node.js process — everything under `dist/client` was compiled at build time. Verify with:

```sh
docker run --rm --entrypoint /bin/sh my-app -c 'ls /app; which node || echo "no node in runtime image"'
```

## Image size tips

- The runtime layer is three directories plus a slim base — typically tens of MB, not hundreds. The heavy toolchain layers stay in the build stage and are not part of the final image.
- Keep `go.sum` and `package-lock.json` committed so the two dependency-cache layers work.
- Build once, promote the same digest through environments; the `dist/client` manifest hashes change per build, so avoid mounting `dist/` from outside the image.

## Compose example

`docker-compose.yml` for local review of the production image:

```yaml
services:
  web:
    build: .
    ports:
      - "3000:3000"
    environment:
      FORM_CSRF_SECRET: "${FORM_CSRF_SECRET:?set FORM_CSRF_SECRET}"
```

Run `docker compose up --build` and open <http://localhost:3000>. For day-to-day development, keep using `meta dev` — it hot-reloads templ, Go, and Svelte, which containers do not.
