# Clerk session recovery

Session recovery is available in **v0.1.0-beta.3** of the independently
versioned `integrations/clerk` module. Updating the Meta framework or CLI alone
does not install it. Upgrade from beta.2 in your application directory:

```sh
go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.3
go mod tidy
```

Remove any local `replace` directive for this integration when switching to the
published module. Existing keyed `Config` literals continue to work. Recovery is
opt-in: upgrading alone keeps the existing sign-in redirect behavior. Follow the
setup below to add the public handlers, uncached loader, browser helper, and
application-owned checking/failure/retry UI. Existing signing-key retries and
`OnKeyFetch` diagnostics are preserved; no SDK version upgrade is required.

## Diagnose before changing the sign-in flow

The recording establishes a slow `/dashboard` response, a 303 to `/sign-in`,
and browser initialization followed by another document request. It does not
establish that an expired cookie caused the redirect. SDK initialization happens
after that first response, so it cannot explain all of its roughly 2.98 seconds.
Cold signing-key fetching/retries, application middleware, or other server work
must be measured separately.

Add the optional diagnostic hook to your existing `metaclerk.Config`:

```go
OnAuthentication: func(ctx context.Context, event metaclerk.AuthenticationEvent) {
    slog.InfoContext(ctx, "Clerk authentication",
        "reason", event.Reason, "source", event.Source, "duration", event.Duration)
},
```

Add `log/slog` and `context` imports. Keep your beta.2 `Retry`, `HTTPClient`, and
`OnKeyFetch` configuration. Both hooks preserve the incoming context for request
correlation. Hooks run synchronously and must be fast and concurrency-safe. The
integration emits no logs unless your hook does so; never add headers, cookies,
tokens, raw errors, or claims to those logs.

| Reason | Meaning |
| --- | --- |
| `verified` | Fresh, signed session with required identity/expiry and accepted claims |
| `missing_credentials` | Neither a usable bearer value nor a session cookie is present |
| `malformed_credentials` | Malformed authorization/token or exceeded input bounds |
| `expired_token` | The pinned SDK verified the signature and reached its expiry check; this is not an authenticated identity |
| `invalid_signature` | Cryptographic signature verification failed |
| `invalid_claims` | Other SDK validation failed or required session/expiry claims are absent |
| `unknown_signing_key` | No matching key in the successfully fetched/cached key set |
| `signing_keys_unavailable` | Key fetching could not complete within its existing retry/cancellation policy |

`Source` is `none`, `cookie`, or `authorization`. `Duration` covers verification,
including signing-key work; compare it with `OnKeyFetch` durations and your
request trace. No token, user/session ID, raw claim, key ID, cookie value, or raw
SDK error is included. The SDK checks expiration before some later issuer/party
checks: an expiry diagnostic does not assert those other checks would pass.
It only makes a browser session check eligible. A fresh token must still pass
every normal verification check before any protected content is rendered.

Client errors remain generic: APIs/actions return 401; signing-key failures
return `503 Authentication temporarily unavailable` rather than pretending the
user signed out. Existing signing-key retries, caching, coalescing, and
`OnKeyFetch` behavior are retained. No dependency version was upgraded.

## Why a browser recovery page

Clerk documents its Go SDK as a
[backend-only SDK](https://clerk.com/docs/guides/development/sdk-development/types).
Inspection of pinned `clerk-sdk-go/v2 v2.8.0` confirms JWT verification and
session-token creation APIs, but no request-handshake handler or session-refresh
client. A backend token-creation API is not proof that a returning browser still
owns a session; this integration does not use it to resurrect expired JWTs.

Clerk's [authentication model](https://clerk.com/docs/guides/how-clerk-works/overview)
describes browser recovery through its Frontend API and a separate full-stack
handshake protocol. The latter needs signed handshake processing, not just a
redirect assembled from a decoded token. This change uses the official browser
[`session.getToken({ skipCache: true })`](https://clerk.com/docs/js-frontend/reference/objects/session)
API and then verifies the **actual browser cookie** on the Go server. It does
not implement a custom refresh-token scheme or full Clerk server handshake.

A fresh valid request still renders immediately. With recovery enabled, a page
request with a missing or signature-verified expired cookie goes to your public
recovery page before interactive sign-in. The browser SDK distinguishes an
active Clerk session from a truly signed-out user. An invalid signature/claim
or bearer-token failure does not enter browser recovery. APIs/actions never
redirect or automatically replay mutations.

## 1. Opt in and mount two public handlers

Keep your existing auth initialization and webhook wiring. Add this field:

```go
RecoveryURL: "/auth/recover",
```

Empty `RecoveryURL` preserves the old sign-in redirect behavior. The configured
recovery URL must be a distinct local path without a query or fragment. Keep
`AuthorizedParties` set to your exact frontend origins.

In `configureAuth`, register the recovery handlers **after** `auth.Middleware()`
and before normal route dispatch. Integrate these cases into your existing
middleware; do not duplicate the auth middleware or remove the webhook case:

```go
recoveryPage := auth.RecoveryPage()
recoveryStatus := auth.RecoveryStatus()
cfg.Middleware = append(cfg.Middleware, auth.Middleware(), func(c fiber.Ctx) error {
    switch c.Path() {
    case "/auth/recover":
        return recoveryPage(c)
    case "/auth/session":
        return recoveryStatus(c)
    // Keep your existing /api/webhooks/clerk case here.
    }
    return c.Next()
})
```

Keep `/auth/recover`, `/auth/session`, and `/sign-in` outside protected page
policies. `RecoveryPage` validates `return_to` and installs public routing data
in the request context; your application owns its rendering. `RecoveryStatus`
responds with 204 only when middleware freshly verified a cookie session. A
bearer token alone cannot pass that check. All recovery responses are private,
no-store; the check returns no identity data and never sets `__session`.

Keep `RequirePageUser()` on protected pages and `RequireUser()` on APIs/actions.
Do not blanket-protect the recovery routes, or a router-level redirect could
run before their loading/error UI is rendered.

## 2. Pass validated routing data to the recovery page

Create `app/loaders/clerk_recovery.go`:

```go
package loaders

import (
    "context"

    metaclerk "github.com/akashKarmakar02/meta/integrations/clerk"
    "github.com/akashKarmakar02/meta/loader"
    "github.com/akashKarmakar02/meta/routecontext"
)

func ClerkRecovery(ctx context.Context, _ routecontext.Route) (metaclerk.Recovery, error) {
    data, ok := metaclerk.RecoveryFromContext(ctx)
    if !ok {
        return data, loader.ErrInvalidInput
    }
    return data, nil
}
```

Register it with your other loaders (using your application's import path):

```go
cfg.Loaders = append(cfg.Loaders,
    loader.Bind("auth/recover/page.svelte", loaders.ClerkRecovery),
)
```

Use a normal uncached loader. It supplies `returnTo`, `signInURL`, `recoveryURL`,
and `retryRequired` through Meta's normal `data` prop on both direct and soft
navigation. Do not read `window.location.search` to find this data in a soft
page's `onMount`: the history commit may still be pending at that moment.

## 3. Copy the browser helper and Svelte example

After resolving the new integration source, locate it with:

```sh
clerk_module_dir="$(go list -m -f '{{.Dir}}' github.com/akashKarmakar02/meta/integrations/clerk)"
cp "$clerk_module_dir/browser/recovery.ts" frontend/clerk-recovery.ts
cp "$clerk_module_dir/examples/RecoveryPage.svelte" frontend/ClerkRecovery.svelte
```

Change the copied component's import from `../browser/recovery` to
`./clerk-recovery`. These files belong to the optional Clerk integration, not
the Meta starter runtime. Future Go module upgrades do not overwrite these
application-owned copies.

The component is a tested Svelte 5 example, with unstyled application-owned
presentation. It immediately displays **“Checking your session…”** while the
SDK initializes and refreshes. On failure or timeout it shows an alert, a
**Try again** button, and a sign-in link. `retryRequired` suppresses automatic
recovery after a failed resume. Unmount cancels pending work so late SDK
results cannot navigate away from a newer page. Customize the copy/styles in
your application; the Go integration renders no UI.

Create `app/auth/recover/page.svelte`:

```svelte
<script lang="ts">
  import ClerkRecovery from '../../../frontend/ClerkRecovery.svelte';
  import { getClerk } from '../../../frontend/clerk-client';
  import { navigateTo } from '../../../frontend/navigation-pipeline';

  let { data } = $props();
</script>

<ClerkRecovery
  {data}
  {getClerk}
  onReady={navigateTo}
  onSignedOut={url => window.location.replace(url)}
/>
```

Use the shared, initialized Clerk instance your application already owns for
`getClerk`. It must return a promise for an initialized object whose `loaded`
is true; `session === null` means signed out, while `undefined` means not ready.
The helper does not initialize another SDK or mount a sign-in component. For a
Svelte provider, return its Clerk object after the provider's readiness signal;
do not create a second instance alongside it.
Keep the checking UI outside a provider that withholds its children until ready,
or configure that provider's loading fallback, so it cannot hide the status.

If your application owns a standalone official JavaScript SDK singleton, a
minimal `frontend/clerk-client.ts` can be:

```ts
import type { RecoveryClerk } from './clerk-recovery';

let initialized: Promise<RecoveryClerk> | undefined;

export function getClerk(): Promise<RecoveryClerk> {
  return initialized ??= (async () => {
    const { Clerk } = await import('@clerk/clerk-js');
    const client = new Clerk(import.meta.env.VITE_CLERK_PUBLISHABLE_KEY);
    await client.load();
    return client;
  })().catch(() => {
    initialized = undefined;
    throw new Error('Clerk initialization unavailable');
  });
}
```

This standalone alternative uses your application's installed
[`@clerk/clerk-js`](https://clerk.com/docs/js-frontend/getting-started/quickstart).
Install it in the application only if that is your chosen SDK; no framework
dependency has been added. Share this getter with other consumers. If you
already initialize Clerk on sign-in/dashboard, adapt their shared singleton
instead of keeping both initialization approaches. No `getClerk()` call or UI
bundle is required on an anonymous SSR landing page. The dynamic SDK import
above runs only when recovery or another explicit consumer calls the getter.

The SDK's optional headless/UI-prefetch controls differ between versions. Core
3 uses [`prefetchUI={false}` on supported providers](https://clerk.com/docs/guides/development/upgrading/upgrade-guides/core-3)
where older SDKs used a headless variant. Use the option your installed SDK
actually exposes; do not upgrade it solely for this helper. This change does
not guarantee that a particular third-party Svelte provider fetches no UI code.

The helper also works from a plain TypeScript module loaded by a templ page.
Call `recoverSession` with explicitly supplied, validated routing data and use
its `ready`, `signed-out`, or `error` result to update your application's UI.
No Svelte, Meta router, DOM mutation, or navigation is imported by the helper.

## 4. Preserve sign-in destinations

The sign-in URL contains Clerk's documented `redirect_url` parameter. Replace
a hard-coded `<SignIn forceRedirectUrl="/dashboard" />` with
`<SignIn fallbackRedirectUrl="/dashboard" />` when you want to honor the intended
destination. Force redirect props override `redirect_url`; see
[Clerk's redirect guidance](https://clerk.com/docs/guides/development/customize-redirect-urls).
Keep Clerk's redirect-origin restrictions configured and do not independently
redirect to an unvalidated query parameter in application code.

The server retains the requested path and query. HTTP requests do not carry
fragments; the neutral helper preserves a fragment if its caller supplies one
in `returnTo`. If fragment restoration matters, retain it in application
navigation state and pass it explicitly rather than inferring it from the
outgoing page's URL during mounting.

## Failure handling and limits

- Missing cookies cannot prove sign-out: the application cannot read Clerk's
  long-lived, HttpOnly client cookie on its Frontend API domain. The SDK checks
  that session. A truly signed-out SDK result proceeds to sign-in; a null token,
  network error, or cookie-check failure remains on retry UI.
- The helper defaults to a 15-second deadline covering SDK initialization,
  refresh, and the cookie probe. It discards late results on timeout/unmount;
  it cannot stop SDK-internal work. Your shared SDK getter must recover from
  failed initialization if you want retries to start another initialization.
- The navigation-only `__meta_clerk_recovery` cookie stores a destination digest
  for two minutes. A repeated failed resume to that destination sets
  `retryRequired`, so the example requires a user action. A successful protected
  request clears it. It is never used to grant authentication. Cookies must
  work for the underlying Clerk session; no cookie means no successful probe.
- A direct SSR request without a fresh cookie needs a redirect/browser step.
  It cannot silently become authenticated on the Go server with this pinned
  SDK. Meta soft navigation can render the recovery page and resume softly
  through its existing redirect/envelope pipeline; no special link opt-out or
  router interception is needed. Other application architectures may still
  require a full document return. Clerk itself may redirect in development,
  satellite-domain, or synchronization flows.
- The example shows loading once its component code loads. For initial-document
  feedback before JavaScript downloads, add an application templ layout with
  fallback content and a `noscript` message. This helper is not an initial-load
  shell. It cannot prevent redirects implemented independently by your app.
- Keep authorization, CSRF, private caching, webhook verification, and API/action
  policies intact. No expired or decoded-but-unverified token authenticates a
  request, and no refresh credential is created or handled by this integration.

## Verification and adoption

Run `go test -race ./...` and `go vet ./...` from `integrations/clerk` (root Go
commands do not cover this module). The repository's `make verify` now includes
those checks, the browser helper/example checks, and the Clerk recovery browser
fixture. Its SDK/FAPI is **simulated**: the fixture uses real Meta SSR/soft
navigation and real signed JWT verification, not a live Clerk tenant.

Before deploying an existing application, update the independent integration,
copy the helper/example, opt in with `RecoveryURL`, mount both public handlers,
bind the uncached loader, reuse the existing SDK singleton, and review forced
sign-in redirects. Run `meta generate`, type checks, and `meta build`, and deploy
the matching server/client assets.
Existing keyed `Config` literals remain compatible; unkeyed literals must be
updated for the new fields.

Still required with a live Clerk development and production instance: returning
after token expiry, an ended/revoked Clerk session, cookie refresh timing and
domain attributes, third-party-cookie restrictions, development/satellite
synchronization, the application's existing Svelte provider, interactive sign-in
redirects, and recovery while offline. Correlate the new diagnostics with Little's
trace before attributing the original delay or redirect to an expired token.
