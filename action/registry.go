package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/akashKarmakar02/meta/routecontext"
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

// csrfFieldName is the single form field name reserved for the framework's
// CSRF token. It is stripped before application decoders run, so decoders
// only ever see their own fields. The value lives here rather than importing
// formcsrf, which would create a cycle risk between the two public packages;
// the transport layer verifies the token before DecodeForm is reached.
const csrfFieldName = "_csrf"

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
// with Bind or BindForm; its zero value is invalid. Binding performs type
// erasure exactly once, at the adapter closure: application code never sees any.
//
// The adapter owns input decoding for transport callers. Invoke decodes a JSON
// request body into the action's concrete input type (strictly: unknown
// fields and trailing values are rejected), runs the action through Execute
// exactly once, and returns the type-erased output. A decode failure returns
// an error wrapping ErrInvalidInput and never invokes the action.
type Binding struct {
	id           string
	invoke       func(context.Context, routecontext.Route, []byte) (any, error)
	decodeForm   func(url.Values) (any, error)
	invokeForm   func(context.Context, routecontext.Route, any) (any, error)
	successURL   string
	formFields   []string
	formRenderer FormRenderer
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

// FormDecoder converts parsed url-encoded form values into the action's
// concrete input type. It runs after CSRF verification and before execution; a
// non-nil error aborts the submission with 400 and never executes the action.
// Applications own all conversion policy (missing vs empty, repeated values,
// numeric parsing) so no reflection or schema system is needed.
type FormDecoder[I any] func(values url.Values) (I, error)

// BindForm registers an action that is additionally callable through
// browser-native url-encoded form submissions. decoder converts form values
// into I; successURL is the Post/Redirect/Get destination served as a 303
// See Other after a successful execution. It must be an absolute same-origin
// path starting with "/" and not "//". The JSON behavior matches Bind
// exactly.
func BindForm[I, O any](id string, act Action[I, O], decoder FormDecoder[I], successURL string) Binding {
	b := Bind(id, act)
	if act == nil || decoder == nil {
		return b
	}
	b.decodeForm = func(values url.Values) (any, error) {
		return decodeFormInput(values, decoder)
	}
	b.invokeForm = func(ctx context.Context, route routecontext.Route, input any) (any, error) {
		in, ok := input.(I)
		if !ok {
			return nil, fmt.Errorf("%w: decoded form input has wrong type", ErrInvalidInput)
		}
		return Execute(ctx, route, in, act)
	}
	b.successURL = successURL
	return b
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

// InvokeDecoded executes the bound action once with an already-decoded input
// value, which must have been produced by DecodeForm for this binding. The
// transport uses it for form submissions where decoding and execution are
// separate steps around CSRF verification. The JSON path stays on Invoke.
func (b Binding) InvokeDecoded(ctx context.Context, route routecontext.Route, input any) (any, error) {
	if b.invokeForm == nil {
		return nil, fmt.Errorf("%w: action %q does not accept form submissions", ErrInvalidInput, b.id)
	}
	return b.invokeForm(ctx, route, input)
}

// decodeFormInput runs the application's typed decoder against parsed form
// values. The CSRF field (csrfFieldName, "_csrf") is removed before the
// decoder runs so application decoders never see framework internals.
func decodeFormInput[I any](values url.Values, decoder FormDecoder[I]) (I, error) {
	filtered := make(url.Values, len(values))
	for k, vs := range values {
		if k == csrfFieldName {
			continue
		}
		filtered[k] = append([]string(nil), vs...)
	}
	return decoder(filtered)
}

// FormCapable reports whether this binding accepts url-encoded form
// submissions. Only BindForm-created bindings do.
func (b Binding) FormCapable() bool {
	return b.decodeForm != nil && b.successURL != ""
}

// DecodeForm converts parsed url-encoded values into the action's typed input.
// The returned value is only valid for FormCapable bindings; otherwise
// ErrInvalidInput is returned.
func (b Binding) DecodeForm(values url.Values) (any, error) {
	if !b.FormCapable() {
		return nil, fmt.Errorf("%w: action %q does not accept form submissions", ErrInvalidInput, b.id)
	}
	return b.decodeForm(values)
}

// SuccessURL returns the configured Post/Redirect/Get destination of a
// form-capable binding.
func (b Binding) SuccessURL() string {
	return b.successURL
}

// InvokeForm executes the bound action with an already-decoded form input.
// The input must have been produced by DecodeForm for the same binding;
// it returns an error for bindings that are not form-capable.
func (b Binding) InvokeForm(ctx context.Context, route routecontext.Route, input any) (any, error) {
	if b.invokeForm == nil {
		return nil, fmt.Errorf("%w: action %q is not form-capable", ErrInvalidInput, b.id)
	}
	return b.invokeForm(ctx, route, input)
}

// ID returns the binding's action ID.
func (b Binding) ID() string {
	return b.id
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
