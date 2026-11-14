# Clerk integration releases

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
