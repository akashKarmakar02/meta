# Middleware and route policies

The starter's `app/hooks.go` configures middleware for the Go server. `Handle` runs for every request after path validation and before Meta's assets, actions, public files, and routes. Call `c.Next()` to continue. Add middleware to `Policies()` to protect individual paths.

```go
package app

import (
    "github.com/akashKarmakar02/meta/server"
    "github.com/gofiber/fiber/v3"
)

func Handle(c fiber.Ctx) error {
    c.Set("X-Content-Type-Options", "nosniff")
    return c.Next()
}

func Policies() []server.Policy {
    return []server.Policy{
        server.Protect("/dashboard/*", requireUser, projectAccess),
        server.Protect("/dashboard/billing", requireUser, projectAdmin),
        server.Protect("/_meta/actions/billing.save", requireUser, projectAdmin),
    }
}

// Replace these examples with real credential and permission checks.
func requireUser(c fiber.Ctx) error { return c.Next() }
func projectAccess(c fiber.Ctx) error { return c.Next() }
func projectAdmin(c fiber.Ctx) error { return c.Next() }
```

Middleware functions execute in the listed order. A function that does not call `c.Next()` stops the chain. The most specific match wins: `/dashboard/billing` uses `requireUser` and `projectAdmin`, while `/dashboard/settings` uses `requireUser` and `projectAccess`. A wildcard such as `/dashboard/*` covers both `/dashboard` and its descendants. Exact paths take precedence over a wildcard at the same path. An unmatched request has no policy.

Patterns must be absolute literal paths, optionally ending in `/*`. Route placeholders such as `:id` and arbitrary globs are not supported. For dynamic paths, protect the containing subtree and inspect `c.Params("id")` in the middleware. A page or API policy runs after route matching and before the loader or handler. Action policies use the action endpoint path; page policies do not automatically protect actions. Duplicate patterns, malformed paths, and empty or nil middleware are rejected at startup. Check policy paths carefully: a valid path that matches no route does not produce an error.

Policies apply to generated page and API routes and registered action endpoints, not to public files or framework assets. Files in `public/` are public and can take precedence over pages. Never place private data there. The global hook runs for those requests too, but authentication and authorization checks must also happen close to the protected data or mutation. Meta does not provide an identity provider, sessions, login, or authorization rules.

For an existing project, create `app/hooks.go` and add the following fields to its `server.Config` in `cmd/app/main.go` (import your app package and Fiber):

```go
Middleware: []fiber.Handler{app.Handle},
Policies: app.Policies(),
```

## Pass request-local values to loaders and actions

`c.Locals` is available to other Fiber middleware, but values used by Go loaders and actions must be put in their request context. Define a shared key and use `requeststate`:

```go
// app/auth/context.go
package auth

import "github.com/akashKarmakar02/meta/requeststate"

type User struct { ID string }
var UserKey = requeststate.NewKey[User]()
```

```go
// Inside middleware, after verifying the credential:
requeststate.Set(c, auth.UserKey, user)
return c.Next()
```

```go
// Inside a loader or action that receives ctx context.Context:
user, ok := requeststate.Get(ctx, auth.UserKey)
if !ok {
    // Reject the request rather than trusting missing identity.
}
```

Values are request-local. Do not store `fiber.Ctx`, request buffers, or other pooled objects in them; use independently owned values. Do not put credentials into a response, log, or client-side loader data.

## Security behavior

Meta does not automatically authenticate requests or protect API routes. Action endpoints retain their trusted-origin check and body limit; URL-encoded forms also require a configured CSRF manager. These checks do not replace authorization. API handlers are responsible for their own input validation, origin/CSRF policy where relevant, authentication, and authorization. Keep the CSRF signing secret persistent and shared by production instances, and use HTTPS.

Matched policies set `Cache-Control: private, no-store` before middleware runs. Avoid overriding it for personalized responses, and configure shared caches and loader cache keys so users cannot receive each other's data. The global hook can set security headers; Meta does not install a restrictive Content Security Policy or HSTS automatically. Test those headers against your deployment before enabling them.

A denial handler controls its own status and body. For JSON actions or APIs, return a JSON error if the client expects JSON. For HTML pages, you may return an error page or a trusted local redirect; soft navigation falls back to a full navigation if a response is not a valid navigation envelope. Never build a redirect from an unvalidated user-supplied URL.
