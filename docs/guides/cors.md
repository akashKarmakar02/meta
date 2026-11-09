# Cross-origin resource sharing (CORS)

Meta does not enable CORS by default. No origin can read your API responses from a browser until you explicitly opt in with an allowed-origin list. Meta ships no CORS engine of its own: it uses [Fiber's `cors` middleware](https://docs.gofiber.io/api/middleware/cors/), registered through the same `Middleware` field as any other application middleware.

CORS is a **browser permission**, not an authentication or authorization mechanism. Approving an origin lets the user's *browser* read a response; it does not identify the caller, authorize a mutation, or replace any server-side check.

## Basic configuration

In `cmd/app/main.go`, import Fiber's cors middleware and add it to your `server.Config`:

```go
import (
    "github.com/gofiber/fiber/v3"
    "github.com/gofiber/fiber/v3/middleware/cors"

    "github.com/akashKarmakar02/meta/server"
)

cfg := server.Config{
    // ...
    Middleware: []fiber.Handler{
        apphooks.Handle,
        cors.New(cors.Config{
            AllowOrigins: []string{
                "https://app.example.com",
            },
            AllowMethods: []string{
                "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS",
            },
            AllowHeaders: []string{
                "Origin", "Content-Type", "Authorization",
            },
        }),
    },
}
```

Application middleware runs in the listed order, after Meta's recovery and observability middleware and before assets, actions, public files, and routes:

```text
Incoming request
    ↓
Meta's built-in recovery/observability
    ↓
Global application middleware (including cors)
    ↓
Generated API route / framework handler
    ↓
Response
```

One consequence of that order: middleware listed *before* the cors handler observes every request, including preflights; middleware listed after it never sees a preflight, because the cors middleware answers preflights itself.

An empty `Middleware` slice means no CORS headers are ever sent — requests from other origins still work at the HTTP level, but browsers refuse to expose the responses to scripts. This is the default and it fails closed.

## Multiple allowed origins

List every frontend origin explicitly. Origins compare exactly (scheme + host + port):

```go
cors.New(cors.Config{
    AllowOrigins: []string{
        "https://app.example.com",
        "https://www.example.com",
        "http://localhost:5173", // local development only
    },
    // ...
})
```

A leading `*.` prefix (for example `https://*.example.com`) allows all subdomains of that domain. Do not use it unless you control every subdomain. The bare wildcard `"*"` approves every origin on the internet and must never be combined with credentials.

## Credentialed requests

Cookies over cross-origin requests need three cooperating settings:

```go
cors.New(cors.Config{
    AllowOrigins:     []string{"https://app.example.com"},
    AllowMethods:     []string{"GET", "POST", "OPTIONS"},
    AllowHeaders:     []string{"Content-Type", "Authorization"},
    AllowCredentials: true,
})
```

- On the client, `fetch(url, { credentials: "include" })` tells the browser to attach cookies for the target origin.
- On the server, `AllowCredentials: true` adds `Access-Control-Allow-Credentials: true` and echoes the **exact** origin in `Access-Control-Allow-Origin`. Wildcards are forbidden here by the CORS specification; Fiber refuses to construct a middleware that combines `AllowOrigins: []string{"*"}` with `AllowCredentials: true` (it panics at startup rather than silently approving everything).
- Browsers only send cookies cross-origin when they are marked `SameSite=None; Secure`, which requires HTTPS in production. Plain `SameSite=Lax` cookies (like Meta's CSRF cookie) stay attached to same-site requests only, so a credentialed cross-origin request simply arrives without them.

The middleware adds `Vary: Origin` to approved responses so shared caches cannot serve one origin's approval to another. Keep it: removing `Vary` from a personalized response lets caches leak one user's data to another. This is why wildcard-plus-credentials is dangerous even in principle — a cacheable `"*"` response can be replayed to any origin with the credentials flag set.

## API-only CORS

Applying CORS globally also approves preflights for pages, actions, and framework endpoints. To restrict it to your file-based APIs, skip non-API paths in the handler's `Next` filter:

```go
cors.New(cors.Config{
    Next: func(c fiber.Ctx) bool {
        return !strings.HasPrefix(c.Path(), "/api/")
    },
    AllowOrigins: []string{"https://app.example.com"},
    // ...
})
```

Preflight (`OPTIONS /api/posts`) carries the same path as the actual request, so the filter covers preflights too — no separate OPTIONS route is needed, and a route without an explicit `OPTIONS` handler still gets correct preflight answers from the middleware. An `OPTIONS` request that is not a preflight (no `Access-Control-Request-Method` header) stays outside CORS scope entirely and continues into your route's normal method handling.

Prefer this pattern when only your JSON API is consumed cross-origin. Server actions under `/_meta/actions/*` then never receive CORS approval headers at all.

## Preflight requests

The browser sends an `OPTIONS` preflight before the actual request whenever the request is not "simple": custom headers such as `Authorization`, content types like `application/json`, or methods like `PATCH`/`PUT`/`DELETE`. The preflight response tells the browser what it may send; Fiber's middleware answers it with `204 No Content` and the approval headers:

```text
OPTIONS /api/posts HTTP/1.1
Origin: https://app.example.com
Access-Control-Request-Method: POST
Access-Control-Request-Headers: content-type, authorization
```

```text
HTTP/1.1 204 No Content
Access-Control-Allow-Origin: https://app.example.com
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Allow-Headers: Origin, Content-Type, Authorization
Access-Control-Max-Age: 600
Vary: Origin, Access-Control-Request-Method, Access-Control-Request-Headers
```

A successful preflight only means the browser *sends* the actual request. It performs no protected operation and implies nothing about authorization: route policies, action trusted-origin checks, and CSRF verification all run against the actual request exactly as before.

Note that the server answers a disallowed preflight without approval headers but still terminates it — the rejection happens in the browser. Endpoint authorization must never rely on the absence of CORS headers; a non-browser client (curl, a script) does not implement CORS at all.

## CORS versus CSRF

Approving an origin and authorizing a mutation are different mechanisms:

| | CORS approval | Authorization |
|---|---|---|
| What it controls | Whether the browser lets a script **read** the response | Whether the server performs the requested effect |
| Enforced by | The browser | Your server code |
| Server actions | Never grants it: `POST /_meta/actions/*` requires the configured trusted origin regardless of CORS | `server.Config.TrustedOrigin` |
| Native forms | Never bypasses the double-submit CSRF token | `server.Config.CSRF` |

Enabling CORS for an origin therefore does not enable cross-origin server actions. A cross-origin action POST that clears CORS still receives the same rejection:

```json
{"ok":false,"error":"cross-origin action request rejected"}
```

Likewise, a cross-origin form submission cannot become valid by adding CORS: it fails the trusted-origin check before CSRF is even consulted, and a same-origin form still needs its signed CSRF token. For an overview of those mechanisms, see [middleware and route policies](middleware.md) and [forms and validation](forms.md).

## Production recommendations

Derive allowed origins from configuration, validate them strictly at startup, and fail closed:

```go
func corsFromEnv() (fiber.Handler, error) {
    var origins []string
    for _, raw := range strings.Split(os.Getenv("CORS_ORIGINS"), ",") {
        origin := strings.TrimSpace(raw)
        if origin == "" {
            continue
        }
        u, err := url.Parse(origin)
        if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
            u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
            return nil, fmt.Errorf("invalid CORS origin %q: use scheme://host[:port]", origin)
        }
        origins = append(origins, origin)
    }
    if len(origins) == 0 {
        return nil, fmt.Errorf("CORS_ORIGINS is required to enable CORS")
    }
    return cors.New(cors.Config{
        AllowOrigins: origins,
        AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
        AllowHeaders: []string{"Origin", "Content-Type", "Authorization"},
        MaxAge:       600,
    }), nil
}
```

- Never accept an origin supplied by the request itself (the `Origin` header, `Referer`, or forwarded headers). Reflection turns your endpoint into an open approval service.
- Reject malformed values loudly instead of skipping them: an ignored typo yields a mysteriously broken frontend.
- Use production HTTPS origins only. Development origins belong in a development environment file, not in the committed default.
- Prefer API-only CORS over global approval.
- Keep `Vary: Origin` intact on approved responses, and `Cache-Control: no-store` on anything personalized — shared caches plus credential approval are how one user reads another's data.

The example server accepts `GO_META_CORS_ORIGINS` (comma-separated explicit origins) and `GO_META_CORS_CREDENTIALS=1` the same way; see `cmd/example/main.go`. Meta never applies these by default in newly created projects — the starter template leaves CORS disabled until you add the middleware yourself.
