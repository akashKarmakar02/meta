package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"go-meta/routecontext"
)

// Registry errors. They follow loader.NewRegistry's conventions (C02):
// actionable sentinels, errors.Is-compatible, no partial registry published.
var (
	// ErrInvalidID reports an action ID that violates the ID grammar.
	ErrInvalidID = errors.New("action: invalid action ID")
	// ErrDuplicateID reports two bindings registered under one ID.
	ErrDuplicateID = errors.New("action: duplicate action ID")
	// ErrNilBinding reports a binding whose zero value reached NewRegistry,
	// which means Bind was handed a nil action.
	ErrNilBinding = errors.New("action: nil binding")
)

// MaxIDLen bounds action IDs so they stay valid single path segments and
// cannot be used to probe path handling with very long inputs.
const MaxIDLen = 128

// ValidID reports whether id satisfies the action ID grammar: 1–128 ASCII
// characters, each a letter, digit, underscore, dot, or hyphen; must start and
// end with a letter or digit. The rule rejects whitespace, control characters,
// slashes, backslashes, percent signs (so no URL-encoded spellings), and any
// non-ASCII byte. It is deliberately narrow so an action ID can never contain
// a path separator, query, or fragment, and never collides with the framework's
// reserved endpoint paths.
func ValidID(id string) bool {
	if len(id) == 0 || len(id) > MaxIDLen {
		return false
	}
	if !isIDRune(id[0]) || !isIDRune(id[len(id)-1]) {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !isIDRune(c) && c != '.' && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func isIDRune(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// Binding associates one stable action ID with a typed action. Construct it
// with Bind; its zero value is invalid. Binding performs type erasure exactly
// once, at the adapter closure: application code never sees any.
//
// The adapter owns input decoding for transport callers. Invoke decodes a JSON
// request body into the action's concrete input type (strictly: unknown
// fields and trailing values are rejected), runs the action through Execute
// exactly once, and returns the type-erased output. A decode failure returns
// an error wrapping ErrInvalidInput and never invokes the action.
type Binding struct {
	id     string
	invoke func(context.Context, routecontext.Route, []byte) (any, error)
}

// Bind adapts a typed Action[I, O] to the registry boundary without invoking
// it. NewRegistry validates the ID and rejects a nil action. No reflection is
// used for invocation: the generic closure calls the action directly.
func Bind[I, O any](id string, act Action[I, O]) Binding {
	if act == nil {
		return Binding{id: id}
	}
	return Binding{id: id, invoke: func(ctx context.Context, route routecontext.Route, body []byte) (any, error) {
		in, err := decodeInput[I](body)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		return Execute(ctx, route, in, act)
	}}
}

// decodeInput strictly decodes a JSON body into a fresh I. Pointer inputs are
// allocated by encoding/json itself (unmarshal into *I where I = *T), so the
// same code path serves struct, pointer, scalar, slice, and named-type inputs.
// An empty or whitespace-only body is rejected explicitly — callers must send
// at least "null"; a null literal decodes to I's zero value by design, keeping
// null distinct from "no body".
func decodeInput[I any](body []byte) (I, error) {
	var in I
	if len(bytes.TrimSpace(body)) == 0 {
		return in, errors.New("empty request body; send \"null\" for no input")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return in, errors.New("multiple JSON values in request body")
		}
		return in, errors.New("trailing data after JSON value")
	}
	return in, nil
}

// Invoke decodes body and executes the bound action once. It is the
// transport-facing type-erased boundary; typed callers use Execute directly.
// An invalid binding (zero value or nil action) is rejected without reading
// the body.
func (b Binding) Invoke(ctx context.Context, route routecontext.Route, body []byte) (any, error) {
	if b.invoke == nil {
		return nil, ErrNilAction
	}
	return b.invoke(ctx, route, body)
}

// Registry is an immutable collection of validated action bindings. Lookup is
// safe for concurrent use; no mutex is required because nothing changes after
// construction. No partial registry is published: NewRegistry returns nil and
// an error on any invalid binding. No request state is retained here.
type Registry struct {
	bindings map[string]Binding
}

// NewRegistry validates every binding before publishing a registry. An empty
// binding list yields an empty, valid registry.
func NewRegistry(bindings ...Binding) (*Registry, error) {
	found := make(map[string]Binding, len(bindings))
	for _, b := range bindings {
		if !ValidID(b.id) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidID, b.id)
		}
		if _, ok := found[b.id]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, b.id)
		}
		if b.invoke == nil {
			return nil, fmt.Errorf("%w: %q", ErrNilBinding, b.id)
		}
		found[b.id] = b
	}
	return &Registry{bindings: found}, nil
}

// Lookup returns the binding for an action ID, or false. It never executes
// the action.
func (r *Registry) Lookup(id string) (Binding, bool) {
	if r == nil {
		return Binding{}, false
	}
	b, ok := r.bindings[id]
	return b, ok
}

// Len reports the number of registered bindings.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.bindings)
}
