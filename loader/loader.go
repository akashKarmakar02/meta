// Package loader defines synchronous, typed server-side data loaders. It does
// not register or run loaders as part of routing; callers invoke them explicitly.
package loader

import (
	"context"
	"errors"

	"go-meta/routecontext"
)

// Loader reads data for a matched route. The context is request-scoped; route
// is the existing defensive routecontext snapshot for that same request.
// Loaders must cooperate with cancellation and synchronize any application-owned
// mutable state they share across concurrent calls.
type Loader[T any] func(ctx context.Context, route routecontext.Route) (T, error)

// These sentinel errors describe outcomes without prescribing HTTP behavior.
// Applications may wrap them; use errors.Is to classify errors. Other errors,
// including context.Canceled and context.DeadlineExceeded, pass through as-is.
var (
	ErrNotFound     = errors.New("loader: not found")
	ErrUnauthorized = errors.New("loader: unauthorized")
	ErrForbidden    = errors.New("loader: forbidden")
	ErrInvalidInput = errors.New("loader: invalid input")
	// ErrNilContext and ErrNilLoader identify invalid Execute calls.
	ErrNilContext = errors.New("loader: nil context")
	ErrNilLoader  = errors.New("loader: nil loader")
)

// RedirectError is a control-flow outcome, not a failure: a loader returns it
// (in the error slot) to ask the dispatch to redirect the request to Location
// instead of rendering. It carries no data result. The dispatch validates the
// location (root-relative only) and responds with a 3xx before rendering. It
// is never logged as a loader failure.
type RedirectError struct {
	// Location is the redirect target. It must be a root-relative path
	// (e.g. "/login"); the dispatch rejects absolute URLs, protocol-relative
	// targets, and other non-root-relative values.
	Location string
}

func (e *RedirectError) Error() string { return "loader: redirect to " + e.Location }

// Redirect returns a RedirectError for the given root-relative location. A
// loader returns it to redirect the request.
func Redirect(location string) error { return &RedirectError{Location: location} }

// Execute calls load once, synchronously, with ctx and the matched route
// snapshot. The caller must supply the snapshot associated with ctx. A nil
// context is rejected. If ctx is already cancelled, Execute returns its error
// before checking load; otherwise a nil loader is rejected. Cancellation during execution is cooperative:
// Execute waits for load to return and preserves its result and error unchanged,
// even if ctx becomes cancelled in the meantime. No goroutine or retry is used.
// The result's zero value is returned for errors detected before invocation.
func Execute[T any](ctx context.Context, route routecontext.Route, load Loader[T]) (T, error) {
	var zero T
	if ctx == nil {
		return zero, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if load == nil {
		return zero, ErrNilLoader
	}
	return load(ctx, route)
}
