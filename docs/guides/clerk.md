# Clerk authentication (experimental)

For the opt-in recovery flow, safe authentication diagnostics, and
“Checking your session…” Svelte example introduced in v0.1.0-beta.3, see
[Clerk session recovery](clerk-recovery.md). Signing-key retries and `OnKeyFetch`
diagnostics from beta.2 remain available. Recovery requires public recovery
handlers and application browser integration.

This separate Go module connects Clerk sessions and signed webhooks to Meta's Fiber server. It has not yet been validated against a live Clerk instance; treat it as experimental and verify the complete sign-in flow before deployment. Install the independent module with `go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.3`.

## Install

```bash
go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.3
```

## 1. Add your keys

From your Meta project root, create `.env.local` for development:

```dotenv
CLERK_SECRET_KEY=sk_test_replace_me
CLERK_WEBHOOK_SIGNING_SECRET=whsec_replace_me
VITE_CLERK_PUBLISHABLE_KEY=pk_test_replace_me
```

Get the API keys from your Clerk dashboard and the signing secret from the Clerk webhook endpoint settings. Use real values, never commit this file, and restart `meta dev` after editing it. Only the publishable key belongs in browser code. For production use deployment environment variables or `.env.production`; see [environment variables](environment.md).

## 2. Create `cmd/app/clerk.go`

This file belongs in **your project**, beside `cmd/app/main.go`. Create it with the following contents:

```go
package main

import (
    "context"
    "errors"
    "os"

    clerk "github.com/clerk/clerk-sdk-go/v2"
    metaclerk "github.com/akashKarmakar02/meta/integrations/clerk"
    "github.com/akashKarmakar02/meta/server"
    "github.com/gofiber/fiber/v3"
)

func configureAuth(cfg *server.Config) error {
    auth, err := metaclerk.New(metaclerk.Config{
        SecretKey: os.Getenv("CLERK_SECRET_KEY"),
        SigningSecret: os.Getenv("CLERK_WEBHOOK_SIGNING_SECRET"),
        SignInURL: "/sign-in",
        AuthorizedParties: []string{"http://localhost:3000"},
    })
    if err != nil {
        return err
    }

    webhook, err := auth.Webhook(metaclerk.WebhookHandlers{
        UserCreated: func(ctx context.Context, eventID string, user clerk.User) error {
            // Replace this with a database upsert and atomic event recording.
            return errors.New("user.created handler not implemented")
        },
        UserUpdated: func(ctx context.Context, eventID string, user clerk.User) error {
            // Replace this with an update that handles duplicate and old events.
            return errors.New("user.updated handler not implemented")
        },
        UserDeleted: func(ctx context.Context, eventID string, user clerk.DeletedResource) error {
            // Replace this with your application's deletion policy.
            return errors.New("user.deleted handler not implemented")
        },
    })
    if err != nil {
        return err
    }

    cfg.Middleware = append(cfg.Middleware, auth.Middleware(), func(c fiber.Ctx) error {
        if c.Path() == "/api/webhooks/clerk" {
            return webhook(c)
        }
        return c.Next()
    })
    cfg.Policies = append(cfg.Policies,
        server.Protect("/dashboard/*", auth.RequirePageUser()),
        server.Protect("/api/private/*", auth.RequireUser()),
        server.Protect("/_meta/actions/post.create", auth.RequireUser()),
    )
    return nil
}
```

Replace the example policy paths with your application's paths. Create a `/sign-in` page using Clerk's browser SDK as described below; configuring `SignInURL` does not create that page. Replace the webhook callbacks with your database code. Until then, they return errors so Clerk can retry instead of silently marking events as processed.

## 3. Call it from `cmd/app/main.go`

Find the existing `cfg := server.Config{ ... }` in `main()`. Add the call **after that block and before** `server.New(cfg)`. Keep the starter's existing routes, middleware, asset configuration, and listening code:

```go
cfg := server.Config{
    // Keep the fields already in your project.
}

if err := configureAuth(&cfg); err != nil {
    log.Fatal(err)
}

// Keep the existing DevOrigin/ClientDir setup here.
app, err := server.New(cfg)
```

Restart `meta dev`. Configure Clerk to send `user.created`, `user.updated`, and `user.deleted` to `https://your-domain/api/webhooks/clerk`. The webhook route must remain public and needs a public tunnel during local development. The integration handles up to 1 MiB of webhook data; also set appropriate server or proxy request-size limits. Unhandled events and nil callbacks receive 204, so only subscribe to events your application handles.

`AuthorizedParties` is an exact list of frontend origins (no trailing slash). Configure production and development origins separately. Tokens without an `azp` claim are rejected, even though Clerk documents that the claim can be absent. Unauthenticated or invalid tokens are treated as signed out; a failed key fetch returns 503. Signing keys are cached and refresh attempts are bounded. `RequirePageUser` redirects only GET and HEAD to a configured local sign-in path; other methods return 401. Use `RequireUser` for APIs and action endpoints. Each protected action needs its own policy; page policies do not protect actions. Authentication does not replace authorization, CSRF checks, or application-specific ownership checks.

## Signing-key retries and diagnostics

Signing-key fetches retry **within the incoming request**, so a successful retry
continues normal session verification. The zero-value `RetryConfig` uses these
per-field defaults:

| Field | Default | Meaning |
|---|---|---|
| `MaxRetries` | `3` | Retries after the initial attempt (four attempts total) |
| `InitialBackoff` | `100 * time.Millisecond` | Doubles between retries: about 100ms, 200ms, 400ms |
| `MaxBackoff` | `time.Second` | Caps backoff including 80–120% jitter |
| `AttemptTimeout` | `time.Second` | Bounds each HTTP attempt, including reading its response |
| `TotalTimeout` | `5 * time.Second` | Bounds all attempts and backoff in a sequence |

Each zero field selects its default, even when other fields are set. Set
`MaxRetries: -1` to disable retries (one attempt, still subject to the timeouts).
Other negative values and a maximum backoff below the initial backoff fail in
`New`. A total timeout shorter than the attempt timeout is valid and takes
precedence. The caller's earlier deadline or cancellation always takes precedence.

```go
// Add context, log/slog, net/http, and time to your imports.
auth, err := metaclerk.New(metaclerk.Config{
    SecretKey: os.Getenv("CLERK_SECRET_KEY"),
    AuthorizedParties: []string{"https://example.com"},
    Retry: metaclerk.RetryConfig{
        MaxRetries:     3,
        InitialBackoff: 100 * time.Millisecond,
        MaxBackoff:     time.Second,
        AttemptTimeout: time.Second,
        TotalTimeout:   5 * time.Second,
    },
    HTTPClient: &http.Client{},
    OnKeyFetch: func(ctx context.Context, event metaclerk.KeyFetchEvent) {
        slog.InfoContext(ctx, "Clerk signing-key fetch",
            "attempt", event.Attempt, "final", event.Final,
            "success", event.Success, "category", event.Category,
            "upstream_status", event.StatusCode, "duration", event.Duration)
    },
})
```

The optional HTTP client is copied by `New`; no process globals are modified.
Its transport must honor request contexts and must not add its own retries.
Redirects are rejected. An injected client's shorter timeout also applies.
`OnKeyFetch` runs synchronously outside the cache mutex: keep it fast and safe
for concurrent calls. It receives one event per attempt and a final sequence
summary. Success means keys were fetched; authentication still requires a valid
token signature and claims. Diagnostics contain only counts, safe categories,
status codes, and durations, never raw errors, bodies, tokens, cookies, or keys.
No diagnostics are emitted unless a hook is configured. Context values from the
initiating request are preserved for a logging handler to extract request IDs;
`InfoContext` alone does not automatically attach them. Do not attach credentials.
Set a request context with Fiber's `c.SetContext` before this middleware to supply
correlation values or cancellation. Fiber's default context does not automatically
cancel on a client disconnect.

Retries cover eligible connection failures, timeouts, and HTTP 429/500/502/503/504.
HTTP 401/403, permanent DNS/TLS/configuration failures, and malformed successful
responses stop immediately. Signature or claims failures do not trigger retries.
For 429/503, valid `Retry-After` delta-seconds or HTTP dates set a minimum wait;
it may exceed `MaxBackoff`. If that wait (or backoff) cannot fit in the remaining
caller/sequence budget, the sequence stops without another attempt. All waits
are cancelable. Exhausted or non-retryable fetch failures keep the existing
503 response: `Authentication temporarily unavailable`.

The **30-second refresh cooldown is separate**: it bounds new fetch sequences
for unknown key IDs and failed refreshes, measured from the start of the last
sequence. It never suppresses retries inside that sequence. Concurrent refreshes
share one sequence per integration instance; the initiating request owns its
context, and cancellation of that request ends the shared sequence. A waiting
request may cancel independently. A later request remains subject to cooldown.
Keys remain fresh for one hour after a successful fetch. Fresh cached keys stay
available during refreshes and after refresh failures; expired keys are never
accepted. A new key ID can refresh after cooldown to handle rotation. There are
no background retry workers or stale-key fallback.

## Svelte integration

Use the community-maintained [Svelte Clerk SDK](https://svelte-clerk.netlify.app/svelte/quickstart) for Meta's client-rendered Svelte pages. Its plain Svelte client provides components such as `ClerkProvider`, `SignIn`, `Show`, and `UserButton`. Meta's Go middleware remains responsible for verifying sessions and protecting server resources; SvelteKit server hooks do not apply to Meta.

Meta mounts Svelte pages separately from layouts. To share a provider from a Svelte layout, use `LayoutOutlet` inside the provider as described in the routing guide. A plain `data-go-layout-outlet` does not share context. The examples below keep each provider within its page and also work without a Svelte layout.

## 4. Install the Svelte SDK

From your project root:

```bash
npm install svelte-clerk
```

The SDK requires Svelte 5.29 or newer. The publishable key in `.env.local` is read by Vite as `import.meta.env.VITE_CLERK_PUBLISHABLE_KEY`. Restart `meta dev` after changing the environment or installing packages. Production builds embed the publishable key, so rerun `meta build` after changing it.

If you followed an earlier version of this guide, remove its `import './clerk'` from `frontend/entry.ts` and remove the old `frontend/clerk.ts` if you no longer use it. Do not initialize both approaches at once.

## 5. Create the sign-in page

Create `app/sign-in/page.svelte` in your project:

```svelte
<script lang="ts">
  import { ClerkProvider, SignIn } from 'svelte-clerk/client';

  const publishableKey = import.meta.env.VITE_CLERK_PUBLISHABLE_KEY;
</script>

<h1>Sign in</h1>
{#if publishableKey}
  <ClerkProvider {publishableKey}>
    <SignIn fallbackRedirectUrl="/dashboard" />
  </ClerkProvider>
{:else}
  <p role="alert">Missing Clerk publishable key.</p>
{/if}
```

Replace `/dashboard` with a real route in your app. The `/sign-in` route must remain public. Clerk's sign-in component requires JavaScript. See [Clerk's sign-in component reference](https://clerk.com/docs/js-frontend/reference/components/authentication/sign-in) for redirect options.

## 6. Show sign-in and account controls

Create `app/account/page.svelte` or adapt this code in another Svelte component:

```svelte
<script lang="ts">
  import { ClerkProvider, Show, SignInButton, UserButton } from 'svelte-clerk/client';

  const publishableKey = import.meta.env.VITE_CLERK_PUBLISHABLE_KEY;
</script>

<h1>Account</h1>
{#if publishableKey}
  <ClerkProvider {publishableKey}>
    <Show when="signed-out">
      <SignInButton />
    </Show>
    <Show when="signed-in">
      <UserButton />
    </Show>
  </ClerkProvider>
{:else}
  <p role="alert">Missing Clerk publishable key.</p>
{/if}
```

`Show` controls browser presentation only. Protect the corresponding page, API routes, and actions with the Go middleware and policies above. For templ SSR pages, use the verified Go session in loaders for user-specific content. The static Svelte SDK quickstart does not authenticate SSR requests; the Go integration handles those. Its session refresh limitation still applies, as described below.

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

Use [Clerk's official JavaScript SDK](https://clerk.com/docs/js-frontend/getting-started/quickstart) for sign-in, sign-out, and session refresh. The browser SDK maintains the short-lived `__session` cookie. With the `RecoveryURL` opt-in, missing or expired cookie sessions can visit a public browser recovery page before interactive sign-in; see the [recovery guide](clerk-recovery.md) for setup, limits, and diagnostics. Without that opt-in, the existing sign-in redirect behavior remains. The pinned Go SDK does not supply a full request-handshake handler, and a JavaScript-disabled browser cannot use this recovery helper. Test your own Clerk instance and existing provider before deployment.

### Local webhook testing with ngrok

`meta dev` accepts only the local development host and port. When ngrok forwards its public hostname unchanged, the development proxy responds with `403 Invalid development host` before the webhook handler runs. Configure ngrok to rewrite the upstream Host header to the exact local host and port. For a Meta server running on port `4000`, create a local `ngrok-policy.yml`:

```yaml
on_http_request:
  - actions:
      - type: add-headers
        config:
          headers:
            host: localhost:4000
```

Then start the tunnel:

```bash
ngrok http 4000 --traffic-policy-file ngrok-policy.yml
```

Use the HTTPS forwarding URL followed by `/api/webhooks/clerk` in Clerk's webhook settings. If your development port differs, change it in both places. The public tunnel can expose other local routes as well; restrict access or use a dedicated test application. See [ngrok's host-header documentation](https://ngrok.com/docs/gateway/endpoints/http/#rewriting-the-host-header).

A `400` response from the handler indicates missing or invalid webhook signature headers, payload, timestamp, or signing secret. A `500` can mean a callback failed; the placeholder callbacks above deliberately return errors until database code is added. A `204` means the verified event was handled or had no configured callback. Check the response body for `Invalid development host` to confirm whether a 403 came from Meta's development proxy.

## Webhook delivery

Webhook callbacks run after Svix signature and timestamp verification. They receive the delivery ID to support deduplication. Events can be duplicated, delayed, or arrive out of order, so use transactional deduplication, ordering or reconciliation appropriate to your data. A signed-in user may reach your app before their webhook is processed; handle a missing local user record. Existing Clerk users also need a backfill. Never log webhook bodies, session tokens, or keys.

## Development

This is an independent Go module. From `integrations/clerk`, run `go test -race ./...` and `go vet ./...`; root-module Go commands do not cover it. Its release tags must use the `integrations/clerk/v...` prefix.
