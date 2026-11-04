// Package action defines typed, synchronous server-side actions: application
// controlled mutations that run exactly once per explicit call.
//
// An action is deliberately not a loader. A loader reads data for a rendering
// request; an action mutates application state on request. That difference
// shapes this contract: the helper invokes an action exactly once, never
// retries, never replays after cancellation, and never rolls back committed
// effects. Nothing in this package registers, exposes, or executes actions
// over HTTP — see the Security section below.
//
// # Usage
//
// Define an action as an ordinary generic function value with typed input and
// output, then call it through Execute:
//
//	type CreatePostInput struct {
//	    Title   string
//	    Content string
//	}
//	type CreatePostResult struct{ ID int64 }
//
//	func CreatePost(ctx context.Context, r routecontext.Route, in CreatePostInput) (CreatePostResult, error) {
//	    if in.Title == "" {
//	        return CreatePostResult{}, action.ErrInvalidInput
//	    }
//	    // ... persist the post ...
//	    return CreatePostResult{ID: 42}, nil
//	}
//
//	result, err := action.Execute(ctx, route, in, CreatePost)
//
// Input and output are strongly typed at the call site: no any, no type
// assertions, no reflection. Any Go type works as input or output — structs,
// pointers, slices, scalars, named types, and empty structs.
//
// # Execution semantics
//
// Execute runs the action synchronously on the calling goroutine, exactly
// once, and returns its result and error unchanged. It rejects nil contexts
// and nil actions, and refuses to invoke an action whose context is already
// cancelled or expired. Cancellation during execution is cooperative: Execute
// waits for the action to return and preserves its outcome; a context-aware
// action observes cancellation through ctx. Panics are never recovered — they
// propagate to the caller. Execute starts no goroutines, holds no locks, and
// shares no state between calls, so independent executions are safe to run
// concurrently.
//
// # Mutation semantics and idempotency
//
// Actions are NOT guaranteed to be idempotent: creating a post twice may
// create two posts. The framework never retries, replays after cancellation,
// or rolls back an action. If cancellation interrupts an action after it has
// committed a side effect (for example, a database write completed before the
// context was observed), the committed effect stands; cancellation is not a
// transaction boundary. Transaction boundaries and application-level
// idempotency remain the responsibility of the action or its application.
//
// An action never runs implicitly. Nothing in this package — or anywhere else
// in go-meta — calls an action during code generation, application startup,
// action registration, route discovery, page rendering, loader lookup, an
// ordinary GET request, CSR bootstrap, soft navigation, or browser prefetching.
// Every execution is an explicit Execute call in application or future
// framework transport code.
//
// # Route context
//
// The route parameter reuses the B05 routecontext.Route snapshot for the
// request that carries the action. Actions read typed path parameters and
// query values from it without reparsing URLs or touching Fiber request
// buffers. Execute accepts any snapshot the caller supplies and does not
// interpret it; future transport layers (D02) may pass a snapshot describing
// an action endpoint's own route identity rather than a page route's.
//
// # Errors
//
// Actions return ordinary Go errors. The sentinels below give common outcomes
// stable names with a forward mapping to HTTP responses in later phases;
// errors remain compatible with errors.Is and errors.As, and wrapped causes
// stay inspectable. Execute converts nothing to HTTP responses, performs no
// redirects, and retries nothing. Raw action error messages must never be sent
// to browsers verbatim; HTTP integration (D02) decides what is exposed.
//
// # Security
//
// This contract provides NO authentication, authorization, CSRF protection,
// input validation, rate limiting, transaction safety, or replay protection.
// Defining an action exposes nothing over HTTP; mutation endpoints arrive in
// D02 and must, before any sensitive mutation becomes reachable: restrict
// endpoints to appropriate methods, reject cross-origin or unauthorized
// submissions, define a CSRF policy, bound request-body sizes, validate
// untrusted input, avoid exposing internal errors, and prevent accidental
// invocation from page loads or prefetches.
//
// This package imports only the standard library and go-meta/routecontext
// (itself stdlib-only via routeparams). It has no Fiber or fasthttp dependency
// and no Node/JavaScript counterpart.
package action

import (
	"context"
	"errors"

	"go-meta/routecontext"
)

// Action is a server-side mutation with typed input and output. The context is
// request-scoped; route is the defensive routecontext snapshot for the same
// request. Actions must cooperate with cancellation and synchronize any
// application-owned mutable state shared across concurrent calls.
type Action[I, O any] func(ctx context.Context, route routecontext.Route, input I) (O, error)

// Sentinel errors name common action outcomes without prescribing HTTP
// behavior. Applications may wrap them; classify with errors.Is. Context
// errors (context.Canceled, context.DeadlineExceeded) pass through as-is.
// They intentionally parallel the loader package's sentinels (C01) so both
// contracts classify with the same vocabulary while remaining independently
// versionable; neither package imports the other.
var (
	ErrNotFound     = errors.New("action: not found")
	ErrUnauthorized = errors.New("action: unauthorized")
	ErrForbidden    = errors.New("action: forbidden")
	ErrInvalidInput = errors.New("action: invalid input")
	// ErrNilContext and ErrNilAction identify invalid Execute calls.
	ErrNilContext = errors.New("action: nil context")
	ErrNilAction  = errors.New("action: nil action")
)

// Execute calls act once, synchronously, with ctx, the supplied route
// snapshot, and input passed through unmodified. It invokes no transport, JSON,
// or validation processing — input belongs entirely to the action, and normal
// Go aliasing rules apply to pointer or reference inputs.
//
// A nil context is rejected (ErrNilContext). If ctx is already cancelled or
// expired, its error is returned without invoking act. A nil action is
// rejected (ErrNilAction). For every rejection the output is O's zero value.
//
// Cancellation during execution is cooperative: Execute waits for act to return
// and preserves its result and error unchanged, even if ctx becomes cancelled
// in the meantime. There is no retry, no caching, no panic recovery, and no
// background execution; act runs on the calling goroutine.
func Execute[I, O any](ctx context.Context, route routecontext.Route, input I, act Action[I, O]) (O, error) {
	var zero O
	if ctx == nil {
		return zero, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if act == nil {
		return zero, ErrNilAction
	}
	return act(ctx, route, input)
}
