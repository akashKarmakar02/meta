// Package loaders — C05 demonstration loaders: redirect and error outcomes.
//
// These are test fixtures for the P2-C05 browser/HTTP suites. They exercise
// the loader error/redirect/cancellation contract end-to-end. They are not
// real business data.
package loaders

import (
	"context"
	"time"

	"go-meta/loader"
	"go-meta/routecontext"
)

// LoadC05RedirectSSR is bound to an SSR route and always redirects to /about.
// It verifies that a loader redirect is honored on a direct SSR request and
// during soft navigation.
func LoadC05RedirectSSR(_ context.Context, _ routecontext.Route) (string, error) {
	return "", loader.Redirect("/about")
}

// LoadC05RedirectCSR is bound to a CSR route and always redirects to
// /dashboard. It verifies that CSR routes honor loader redirects too.
func LoadC05RedirectCSR(_ context.Context, _ routecontext.Route) (string, error) {
	return "", loader.Redirect("/dashboard")
}

// LoadC05SlowRedirect is bound to an SSR route and sleeps before redirecting
// to /docs. The delay lets a browser test start a second navigation while the
// first is still in flight, proving a stale redirect can never commit. The
// target is /docs (not /about) so the stale-redirect test's fast target stays
// a distinct URL: if the stale redirect ever committed, the final page would
// be /docs, not the fast target.
func LoadC05SlowRedirect(ctx context.Context, _ routecontext.Route) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(400 * time.Millisecond):
	}
	return "", loader.Redirect("/docs")
}

// LoadC05NotFound is bound to a route and always reports a not-found outcome.
// It verifies that a loader error prevents rendering and maps to HTTP 404.
func LoadC05NotFound(_ context.Context, _ routecontext.Route) (string, error) {
	return "", loader.ErrNotFound
}
