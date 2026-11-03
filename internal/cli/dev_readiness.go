package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Readiness contract (A06 §4): a spawned subprocess proves nothing. The
// public proxy is ready only when it answers on the public origin; the
// backend is ready only when /healthz answers through the proxy (which
// exercises proxy -> Unix socket -> Fiber in one request); Vite is ready
// only when its client module answers through the same origin (which also
// proves the HMR route prefix wiring, since the tag is on the prefix).
//
// Checks are bounded (deadline, short per-request timeout) and stop as soon
// as everything is up — there is no background polling after success.
func waitForDevReady(ctx context.Context, base string, deadline time.Duration) error {
	publicURL := base + "/healthz"
	viteURL := base + vitePrefix + "@vite/client"
	if err := pollUntilUp(ctx, publicURL, 200, "public development server", deadline); err != nil {
		return err
	}
	return pollUntilUp(ctx, viteURL, 200, "Vite development server", deadline)
}

// pollUntilUp polls url until it returns exactly want status, then returns.
// Any other status keeps polling until the deadline; the deadline turns
// into an error naming the service that never came up. ctx cancellation
// aborts immediately (Ctrl+C during startup must not hang on a probe).
func pollUntilUp(ctx context.Context, url string, want int, what string, deadline time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	end := time.Now().Add(deadline)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == want {
				return nil
			}
		}
		if !time.Now().Before(end) {
			return fmt.Errorf("%s did not become ready at %s within %s", what, url, deadline)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
