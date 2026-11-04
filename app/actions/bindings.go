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
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"go-meta/action"
	"go-meta/app/guestbookform"
	"go-meta/routecontext"
)

// Calls counts invocations of the demo actions so tests can assert exact
// execution counts (exactly-once per accepted request, zero on rejections).
var Calls struct {
	Create          atomic.Int64
	Echo            atomic.Int64
	Fail            atomic.Int64
	Slow            atomic.Int64
	Guestbook       atomic.Int64
	UpdateCacheDemo atomic.Int64
}

// Reset zeroes the invocation counters and clears the guestbook demo store
// (test helper).
func Reset() {
	Calls.Create.Store(0)
	Calls.Echo.Store(0)
	Calls.Fail.Store(0)
	Calls.Slow.Store(0)
	Calls.Guestbook.Store(0)
	Calls.UpdateCacheDemo.Store(0)
	guestbookMu.Lock()
	guestbook = nil
	guestbookNext.Store(0)
	guestbookMu.Unlock()
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

// GuestbookEntry is one submission to the D03 no-JS form demo. The store is an
// in-memory demo target: it proves exactly-once execution and shows submitted
// values on the destination page, but it is not persistence — restarts clear
// it and it is explicitly not a database.
type GuestbookEntry struct {
	Name    string
	Message string
}

var (
	guestbookMu    sync.RWMutex
	guestbookNext  atomic.Int64
	guestbookMaxID int64
)

// Guestbook returns a copy of all entries in insertion order.
func Guestbook() []GuestbookEntry {
	guestbookMu.RLock()
	defer guestbookMu.RUnlock()
	out := make([]GuestbookEntry, len(guestbook))
	copy(out, guestbook)
	return out
}

var guestbook []GuestbookEntry

// SubmitGuestbook is the D03 form-action fixture: typed url-encoded input,
// concurrency-safe in-memory mutation, PRG redirect destination /guestbook.
func SubmitGuestbook(_ context.Context, _ routecontext.Route, in GuestbookEntry) (struct{}, error) {
	Calls.Guestbook.Add(1)
	fields := map[string][]string{}
	if strings.TrimSpace(in.Name) == "" {
		fields["name"] = []string{"Name is required"}
	} else if len(in.Name) > 80 {
		fields["name"] = []string{"Name must be at most 80 characters"}
	}
	if strings.TrimSpace(in.Message) == "" {
		fields["message"] = []string{"Message is required"}
	} else if len(in.Message) > 500 {
		fields["message"] = []string{"Message must be at most 500 characters"}
	}
	if len(fields) > 0 {
		return struct{}{}, action.NewValidationError("Please correct the highlighted fields.", fields)
	}
	guestbookMu.Lock()
	defer guestbookMu.Unlock()
	id := guestbookNext.Add(1)
	if id > guestbookMaxID {
		guestbookMaxID = id
	}
	guestbook = append(guestbook, GuestbookEntry{Name: in.Name, Message: in.Message})
	return struct{}{}, nil
}

// DecodeGuestbookForm is the D03 typed form decoder: explicit conversion of
// parsed url.Values into GuestbookEntry. Repeated keys, missing fields, and
// non-ASCII input each have defined behavior here because the application owns
// all conversion policy.
func DecodeGuestbookForm(values url.Values) (GuestbookEntry, error) {
	var in GuestbookEntry
	names := values["name"]
	messages := values["message"]
	if len(names) > 1 {
		return in, fmt.Errorf("field %q may only appear once", "name")
	}
	if len(messages) > 1 {
		return in, fmt.Errorf("field %q may only appear once", "message")
	}
	if len(names) == 0 {
		return in, fmt.Errorf("field %q is required", "name")
	}
	if len(messages) == 0 {
		return in, fmt.Errorf("field %q is required", "message")
	}
	in.Name = names[0]
	in.Message = messages[0]
	return in, nil
}

// Bindings returns application action bindings. D02 registration is explicit:
// nothing executes here, and duplicate IDs fail at registry construction with
// an actionable startup diagnostic. BindForm opts a binding into D03 HTML form
// submission with a typed decoder and a trusted PRG destination.
func Bindings() []action.Binding {
	return []action.Binding{
		action.Bind("demo.createPost", CreatePost),
		action.Bind("demo.echo", EchoPointer),
		action.Bind("demo.fail", Fail),
		action.Bind("demo.slow", Slow),
		action.BindForm("guestbook.submit", SubmitGuestbook, DecodeGuestbookForm, "/guestbook").WithFormFeedback([]string{"name", "message"}, guestbookform.Feedback),
		action.Bind("cache-demo.update", UpdateCacheDemo),
	}
}
