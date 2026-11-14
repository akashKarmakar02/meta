# Clerk integration releases

## v0.1.0-beta.3 — 2026-11-14

- Add opt-in `Config.RecoveryURL` and public `RecoveryPage`/`RecoveryStatus`
  handlers. Missing or signature-verified expired cookie sessions can recover
  through the application's ClerkJS instance before interactive sign-in.
- Add redacted `Config.OnAuthentication` diagnostics distinguishing missing,
  malformed, expired, signature/claims, unknown-key, and key-unavailable outcomes.
  Keep generic client errors and all beta.2 signing-key retries/diagnostics.
- Add an optional dependency-free browser helper using ClerkJS `getToken` with
  `skipCache: true`, a fresh server cookie check, timeout/cancellation, and a
  Svelte loading/failure/retry example. Preserve local destinations and bound
  failed resume loops; never authenticate from decoded or expired claims.
- Add independent-module, browser helper/component, and real Meta browser tests
  with simulated ClerkJS/FAPI. Extend `make verify` to cover this module.

Clerk Go SDK remains pinned at v2.8.0; no dependency version was upgraded. The
already-present go-jose dependency is now direct for typed verification error
classification. Recovery remains opt-in and requires application adoption; see
the [migration/setup guide](../../docs/guides/clerk-recovery.md). Live Clerk
session/cookie synchronization and provider behavior still require validation.

### Upgrade

```sh
go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.3
go mod tidy
```

Updating the framework or CLI alone does not update this independent module.
Existing keyed `Config` literals remain compatible. Recovery requires explicit
`RecoveryURL` configuration, public handlers, an uncached application loader,
and adoption of the browser helper/example. See the [migration guide](../../docs/guides/clerk-recovery.md)
for complete steps; unkeyed config literals need the new fields.

### Validation and limitations

- `make verify` passed, including independent `go test -race ./...` and
  `go vet ./...`, root Go checks, Svelte/TypeScript checks, production builds,
  formatting/diff hygiene, 302 frontend passes (one optional skip), 149 Chromium
  passes (12 native-image skips), and seven CORS passes.
- Recovery tests use signed fixture tokens and simulated ClerkJS/FAPI with real
  Meta direct and soft navigation. A live Clerk tenant is still needed to verify
  refresh/cookie synchronization, provider behavior, and development/satellite
  redirects. The original recording's root cause remains unconfirmed.
- Recovery requires browser initialization; the pinned Go SDK has no full request
  handshake handler. No expired or unverified token authenticates a request.

## v0.1.0-beta.2 — 2026-11-14

Signing-key fetches now retry within the incoming HTTP request. A successful
retry continues normal session verification; exhausted or permanent fetch
failures retain the generic `503 Authentication temporarily unavailable` response.

### Changes

- Add `Config.Retry` with three retries after the initial attempt, approximately
  100/200/400ms exponential backoff with bounded jitter, a one-second attempt
  timeout, and a five-second total deadline. Earlier caller deadlines and
  cancellation take precedence. Each zero field selects its default;
  `MaxRetries: -1` disables retries. Invalid configuration fails in `New`.
- Retry eligible connection failures, timeouts, and HTTP 429/500/502/503/504.
  Respect Retry-After seconds and HTTP dates on 429/503. Close failed bodies
  before retrying and make backoff waits cancelable.
- Coalesce refreshes per integration instance without holding the cache mutex
  during network calls or waits. Fresh cached keys remain usable during refresh
  failures; expired keys are never accepted. The existing 30-second refresh
  cooldown bounds new sequences independently of retries within a sequence.
- Add optional `Config.HTTPClient` and contextual `Config.OnKeyFetch` diagnostics
  containing only attempt counts, safe categories, upstream status, duration,
  and final success/failure. No process globals are modified.
- Add behavioral tests for retries, cancellation, deadlines, Retry-After,
  coalescing, cache access, token verification, redaction, and unchanged errors.

No dependencies were upgraded; Clerk SDK remains pinned at v2.8.0.

### Upgrade

```sh
go get github.com/akashKarmakar02/meta/integrations/clerk@v0.1.0-beta.2
```

Existing keyed `Config` literals need no changes; retries are enabled by default.
Unkeyed literals must account for the new fields. See the [README](README.md#signing-key-retries-and-diagnostics)
for all configuration defaults and a diagnostic hook example. The integration is
versioned independently of the Meta framework and CLI.

### Validation and limitations

- Independent module: `go test -race ./...` and `go vet ./...` passed.
- Repository: `make verify` passed, including Go checks/build, frontend checks,
  248 unit-test passes (one skip), 132 Chromium passes (12 skips), and seven CORS
  passes.
- Not validated against a live Clerk instance. Full session handshake/refresh
  remains unimplemented. Shared refreshes use the initiating request's context;
  Fiber disconnect cancellation requires an application-supplied context.
