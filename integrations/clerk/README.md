# Clerk integration for Meta (experimental)

This separate Go module connects Clerk sessions and signed webhooks to Meta's Fiber server. It has not yet been validated against a live Clerk instance; treat it as experimental and verify the complete sign-in flow before deployment. Install the independent module with `go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.1`.

## Configure the server

Set `CLERK_SECRET_KEY`, `CLERK_WEBHOOK_SIGNING_SECRET`, and `VITE_CLERK_PUBLISHABLE_KEY` in your environment. Only the publishable key belongs in browser code. See [Clerk's Go session guide](https://clerk.com/docs/guides/sessions/verifying) and [webhook setup](https://clerk.com/docs/guides/development/webhooks/syncing).

Initialize once when creating `server.Config` (error handling shown explicitly):

```go
import (
    "context"
    "log"
    "os"

    clerk "github.com/clerk/clerk-sdk-go/v2"
    metaclerk "github.com/akashKarmakar02/meta/integrations/clerk"
    "github.com/akashKarmakar02/meta/server"
    "github.com/gofiber/fiber/v3"
)

func configureAuth(cfg *server.Config) {
    auth, err := metaclerk.New(metaclerk.Config{
        SecretKey: os.Getenv("CLERK_SECRET_KEY"),
        SigningSecret: os.Getenv("CLERK_WEBHOOK_SIGNING_SECRET"),
        SignInURL: "/sign-in",
        AuthorizedParties: []string{"http://localhost:3000"},
    })
    if err != nil { log.Fatal(err) }

    webhook, err := auth.Webhook(metaclerk.WebhookHandlers{
        UserCreated: func(ctx context.Context, eventID string, user clerk.User) error {
            // Upsert user.ID in your database, then record eventID atomically.
            return nil
        },
        UserUpdated: func(ctx context.Context, eventID string, user clerk.User) error {
            // Update your database, handling old or duplicate events.
            return nil
        },
        UserDeleted: func(ctx context.Context, eventID string, user clerk.DeletedResource) error {
            // Apply your application's deletion policy.
            return nil
        },
    })
    if err != nil { log.Fatal(err) }

    cfg.Middleware = append(cfg.Middleware, auth.Middleware(), func(c fiber.Ctx) error {
        if c.Path() == "/api/webhooks/clerk" { return webhook(c) }
        return c.Next()
    })
    cfg.Policies = append(cfg.Policies,
        server.Protect("/dashboard/*", auth.RequirePageUser()),
        server.Protect("/api/private/*", auth.RequireUser()),
        server.Protect("/_meta/actions/post.create", auth.RequireUser()),
    )
}
```

Replace the placeholder callbacks with database operations before using the webhook. Configure Clerk to send the `user.created`, `user.updated`, and `user.deleted` events to the public HTTPS endpoint. Do not protect the webhook endpoint with a sign-in policy. For local development, Clerk needs an externally reachable tunnel. Unhandled events and nil callbacks receive 204; do not subscribe to events you intend to process until their callbacks are configured. The integration handles up to 1 MiB of webhook data; also set appropriate server or proxy request-size limits.

`AuthorizedParties` is an exact list of frontend origins (no trailing slash). Configure production and development origins separately. Tokens without an `azp` claim are rejected, even though Clerk documents that the claim can be absent. Unauthenticated or invalid tokens are treated as signed out; a failed key fetch returns 503. Signing keys are cached and refresh attempts are bounded. `RequirePageUser` redirects only GET and HEAD to a configured local sign-in path; other methods return 401. Use `RequireUser` for APIs and action endpoints. Each protected action needs its own policy; page policies do not protect actions. Authentication does not replace authorization, CSRF checks, or application-specific ownership checks.

## Read the identity

Inside a loader, action, or other handler with a request context:

```go
session, ok := metaclerk.SessionFromContext(ctx)
if !ok {
    // Reject an unauthenticated request.
}
userID := session.UserID
_ = userID
```

Only verified user and session IDs are exposed. Protect personalized responses from shared caching and configure loader caching accordingly. Route policies set private, no-store headers, but global middleware and unprotected personalized routes need equivalent safeguards.

## Browser and session limitations

Use [Clerk's official JavaScript SDK](https://clerk.com/docs/js-frontend/getting-started/quickstart) for sign-in, sign-out, and session refresh. Configure its publishable key for Vite and initialize it on pages where it is needed. Clerk's browser SDK maintains the short-lived `__session` cookie. A direct SSR request with a missing or expired cookie will be treated as signed out; this package does not yet implement Clerk's full request handshake or refresh flow. A JavaScript-disabled browser cannot refresh that cookie through Clerk's browser SDK. Test expired tokens, direct navigation, soft navigation, and forms with your Clerk setup.

## Webhook delivery

Webhook callbacks run after Svix signature and timestamp verification. They receive the delivery ID to support deduplication. Events can be duplicated, delayed, or arrive out of order, so use transactional deduplication, ordering or reconciliation appropriate to your data. A signed-in user may reach your app before their webhook is processed; handle a missing local user record. Existing Clerk users also need a backfill. Never log webhook bodies, session tokens, or keys.

## Development

This is an independent Go module. Run `go test ./...` from this directory; root-module Go commands do not cover it. Its release tags must use the `integrations/clerk/v...` prefix.
