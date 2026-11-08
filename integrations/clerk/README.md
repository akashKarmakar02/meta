# Clerk authentication (experimental)

This separate Go module connects Clerk sessions and signed webhooks to Meta's Fiber server. It has not yet been validated against a live Clerk instance; treat it as experimental and verify the complete sign-in flow before deployment. Install the independent module with `go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.1`.

## Install

```bash
go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.1
```

## 1. Add your keys

From your Meta project root, create `.env.local` for development:

```dotenv
CLERK_SECRET_KEY=sk_test_replace_me
CLERK_WEBHOOK_SIGNING_SECRET=whsec_replace_me
VITE_CLERK_PUBLISHABLE_KEY=pk_test_replace_me
```

Get the API keys from your Clerk dashboard and the signing secret from the Clerk webhook endpoint settings. Use real values, never commit this file, and restart `meta dev` after editing it. Only the publishable key belongs in browser code. For production use deployment environment variables or `.env.production`; see [environment variables](../../docs/guides/environment.md).

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
    <SignIn forceRedirectUrl="/dashboard" />
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

Use [Clerk's official JavaScript SDK](https://clerk.com/docs/js-frontend/getting-started/quickstart) for sign-in, sign-out, and session refresh. Configure its publishable key for Vite and initialize it on pages where it is needed. Clerk's browser SDK maintains the short-lived `__session` cookie. A direct SSR request with a missing or expired cookie will be treated as signed out; this package does not yet implement Clerk's full request handshake or refresh flow. A JavaScript-disabled browser cannot refresh that cookie through Clerk's browser SDK. Test expired tokens, direct navigation, soft navigation, and forms with your Clerk setup.

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

This is an independent Go module. Run `go test ./...` from this directory; root-module Go commands do not cover it. Its release tags must use the `integrations/clerk/v...` prefix.
