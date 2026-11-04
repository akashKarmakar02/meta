// Package actions holds explicit bindings between stable action IDs and
// typed application actions. IDs are dot-separated names ("demo.createPost") —
// see action.ValidID for the grammar; they are independent of page routes.
//
// The demo.* actions are D02 fixtures: they exercise the typed input/output
// matrix (struct, pointer, null result), the error mapping (invalid input,
// unauthorized, forbidden, not found, unexpected), and cancellation through a
// real registered endpoint. Each records invocations so tests can prove
// exactly-once execution and zero execution on GET/navigation.
package actions

import (
	"context"
	"errors"
	"sync/atomic"

	"go-meta/action"
	"go-meta/routecontext"
)

// Calls counts invocations of the demo actions so tests can assert exact
// execution counts (exactly-once per accepted request, zero on rejections).
var Calls struct {
	Create atomic.Int64
	Echo   atomic.Int64
	Fail   atomic.Int64
	Slow   atomic.Int64
}

// Reset zeroes the invocation counters (test helper).
func Reset() {
	Calls.Create.Store(0)
	Calls.Echo.Store(0)
	Calls.Fail.Store(0)
	Calls.Slow.Store(0)
}

type CreatePostInput struct {
	Title string `json:"title"`
}

type CreatePostResult struct {
	ID int64 `json:"id"`
}

// CreatePost is the D02 happy-path fixture: struct in, struct out.
func CreatePost(_ context.Context, _ routecontext.Route, in CreatePostInput) (CreatePostResult, error) {
	Calls.Create.Add(1)
	if in.Title == "" {
		return CreatePostResult{}, errors.New("title required")
	}
	return CreatePostResult{ID: 42}, nil
}

type EchoInput struct {
	Message string `json:"message"`
}

// EchoPointer demonstrates a pointer-output action: a nil result marshals to a
// legitimate JSON null, distinct from an error.
func EchoPointer(_ context.Context, _ routecontext.Route, in EchoInput) (*EchoInput, error) {
	Calls.Echo.Add(1)
	if in.Message == "" {
		return nil, nil // legitimate null result
	}
	return &in, nil
}

// Fail demonstrates every D02 error mapping. The input selects the outcome:
// "unauthorized", "forbidden", "notfound" map to their sentinels; "boom"
// produces an unexpected internal error; "" is invalid input.
func Fail(_ context.Context, _ routecontext.Route, in EchoInput) (EchoInput, error) {
	Calls.Fail.Add(1)
	switch in.Message {
	case "unauthorized":
		return EchoInput{}, action.ErrUnauthorized
	case "forbidden":
		return EchoInput{}, action.ErrForbidden
	case "notfound":
		return EchoInput{}, action.ErrNotFound
	case "boom":
		return EchoInput{}, errors.New("internal: secret detail")
	case "":
		return EchoInput{}, action.ErrInvalidInput
	default:
		return in, nil
	}
}

// Slow blocks until the request context is done, then returns its cancellation
// error — the cancellation fixture: the client aborts mid-action, the context
// unblocks, and dispatch maps the outcome without executing anything twice.
func Slow(ctx context.Context, _ routecontext.Route, in EchoInput) (EchoInput, error) {
	Calls.Slow.Add(1)
	<-ctx.Done()
	return EchoInput{}, ctx.Err()
}

// Bindings returns application action bindings. D02 registration is explicit:
// nothing executes here, and duplicate IDs fail at registry construction with
// an actionable startup diagnostic.
func Bindings() []action.Binding {
	return []action.Binding{
		action.Bind("demo.createPost", CreatePost),
		action.Bind("demo.echo", EchoPointer),
		action.Bind("demo.fail", Fail),
		action.Bind("demo.slow", Slow),
	}
}
