// Package routeparams provides a small, request-safe API for reading named
// path parameters captured by go-meta's generated routing.
//
// A Params value is an immutable view of the parameters for a single HTTP
// request. It is attached to the render context by the framework (see
// internal/httpserver.WithRouteParams) and read by application code — for
// example a templ page — via FromContext:
//
//	params := routeparams.FromContext(ctx)
//	id, err := params.Int64("userId")
//
// Params is deliberately small: string access plus strict numeric and boolean
// conversions. It is server-side only; there is no client-side (JavaScript)
// counterpart in this release. Query parameters, loaders, and the broader
// route context are out of scope (they belong to later Phase 2 tasks).
//
// The package imports only the standard library. It never imports Fiber or
// fasthttp and never exposes a Fiber request object: the framework-facing
// entry point (Attach) accepts a minimal value-storage interface that the
// fasthttp request context satisfies.
package routeparams

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// Sentinel errors for parameter access. Use errors.Is to recognize a missing
// or invalid parameter regardless of the parameter name or value embedded in
// the message:
//
//	if _, err := params.Int64("userId"); errors.Is(err, routeparams.ErrMissingParam) {
//		// the parameter was not present in the route.
//	}
//
// A missing parameter and an invalid (present but unparseable) parameter are
// distinct: missing means the route did not capture a value for the name,
// while invalid means a value was captured but could not be converted.
var (
	// ErrMissingParam is returned (wrapped) when a parameter name is not
	// present in the request's captured parameters.
	ErrMissingParam = errors.New("routeparams: parameter missing")
	// ErrInvalidParam is returned (wrapped) when a captured parameter value
	// cannot be converted to the requested type.
	ErrInvalidParam = errors.New("routeparams: invalid parameter value")
)

// key is the private, collision-resistant context key under which a Params
// value is stored. Application code must not construct its own key or look up
// parameters by an arbitrary string key; it must use FromContext.
type key struct{}

// Params is an immutable, request-local view of the named path parameters
// captured for a single request. It is safe for concurrent use by multiple
// goroutines reading the same request's parameters, and it never exposes the
// framework's mutable request-local storage.
//
// A zero Params (or one obtained from FromContext when no parameters were
// attached) has no parameters: every Get returns ("", false) and every
// conversion returns a missing-parameter error.
type Params struct {
	values map[string]string
}

// NewParams returns a Params holding a defensive copy of values. It is
// primarily for tests and for the framework's attachment path; application
// code should obtain a Params from FromContext instead of constructing one.
// A nil values map is treated as empty.
func NewParams(values map[string]string) Params {
	if len(values) == 0 {
		return Params{}
	}
	copied := make(map[string]string, len(values))
	for k, v := range values {
		copied[k] = v
	}
	return Params{values: copied}
}

// Get returns the string value for name and whether the parameter is present.
// It performs no conversion. An empty name is always reported as missing.
// A present parameter with an empty value is distinct from a missing one:
// Get returns ("", true) for a present empty value and ("", false) for a
// missing name.
func (p Params) Get(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	v, ok := p.values[name]
	return v, ok
}

// All returns a defensive copy of every captured parameter. The returned map
// is owned by the caller; mutating it does not affect the Params or the
// framework's request-local storage. A Params with no parameters returns a
// non-nil empty map.
func (p Params) All() map[string]string {
	out := make(map[string]string, len(p.values))
	for k, v := range p.values {
		out[k] = v
	}
	return out
}

// Int64 returns the parameter for name parsed as a signed 64-bit integer.
// The value is parsed with strconv.ParseInt at base 10: an optional leading
// sign and decimal digits are accepted, so "42", "-42", and "+42" are valid.
// A missing name yields an error satisfying errors.Is(err, ErrMissingParam);
// a present but non-numeric value, or one outside int64 range, yields an
// error satisfying errors.Is(err, ErrInvalidParam).
func (p Params) Int64(name string) (int64, error) {
	s, err := p.require(name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, invalid(name, s, err)
	}
	return n, nil
}

// Int returns the parameter for name parsed as a native int, bounded by the
// architecture's int width (strconv.IntSize). It behaves like Int64 but fails
// for values outside the native int range, so it is safe on both 32-bit and
// 64-bit platforms.
func (p Params) Int(name string) (int, error) {
	s, err := p.require(name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(s, 10, strconv.IntSize)
	if err != nil {
		return 0, invalid(name, s, err)
	}
	return int(n), nil
}

// Uint64 returns the parameter for name parsed as an unsigned 64-bit integer.
// A leading sign is not permitted: a negative value such as "-1" fails rather
// than wrapping, and a value outside uint64 range fails. Both failures yield
// an error satisfying errors.Is(err, ErrInvalidParam).
func (p Params) Uint64(name string) (uint64, error) {
	s, err := p.require(name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, invalid(name, s, err)
	}
	return n, nil
}

// Bool returns the parameter for name parsed as a boolean using
// strconv.ParseBool's accepted spellings ("1", "t", "T", "true", "TRUE",
// "True", "0", "f", "F", "false", "FALSE", "False"). Any other value fails
// with an error satisfying errors.Is(err, ErrInvalidParam). There is no
// JavaScript-style truthiness: only the documented spellings are accepted.
func (p Params) Bool(name string) (bool, error) {
	s, err := p.require(name)
	if err != nil {
		return false, err
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, invalid(name, s, err)
	}
	return b, nil
}

// require returns the value for name or a missing-parameter error. It is the
// single point where a missing name is distinguished from a present value, so
// no conversion can silently turn an absent parameter into a zero value.
func (p Params) require(name string) (string, error) {
	if name == "" {
		return "", missing("")
	}
	v, ok := p.values[name]
	if !ok {
		return "", missing(name)
	}
	return v, nil
}

// missing wraps ErrMissingParam with the parameter name.
func missing(name string) error {
	return &paramError{sentinel: ErrMissingParam, name: name, value: "", cause: nil}
}

// invalid wraps ErrInvalidParam with the parameter name and the offending
// value. The value is included only when it is safe to surface (it is a
// captured path segment, already validated by the framework, and never a full
// request URL).
func invalid(name, value string, cause error) error {
	return &paramError{sentinel: ErrInvalidParam, name: name, value: value, cause: cause}
}

// paramError is the concrete error returned by the conversion methods. It
// satisfies errors.Is for the matching sentinel and carries the name, the
// offending value, and the underlying strconv cause (for invalid values).
type paramError struct {
	sentinel error
	name     string
	value    string
	cause    error
}

func (e *paramError) Error() string {
	switch e.sentinel {
	case ErrInvalidParam:
		return fmt.Sprintf("routeparams: invalid value for parameter %q: %q", e.name, e.value)
	default:
		return fmt.Sprintf("routeparams: missing parameter %q", e.name)
	}
}

// Unwrap returns the sentinel so errors.Is(err, ErrMissingParam) and
// errors.Is(err, ErrInvalidParam) match, plus the underlying strconv cause for
// invalid values so callers can further classify it (for example a numeric
// overflow via errors.As(err, &*strconv.NumError)). Returning a slice keeps
// both in the error chain.
func (e *paramError) Unwrap() []error {
	errs := []error{e.sentinel}
	if e.cause != nil {
		errs = append(errs, e.cause)
	}
	return errs
}

// FromContext returns the Params attached to ctx by the framework, or an
// empty Params if none was attached. It never panics and never returns nil.
//
// In a templ component, ctx is the render context (the fasthttp request
// context), so this is the standard way to read route parameters:
//
//	params := routeparams.FromContext(ctx)
func FromContext(ctx context.Context) Params {
	if ctx == nil {
		return Params{}
	}
	if p, ok := ctx.Value(key{}).(Params); ok {
		return p
	}
	return Params{}
}

// WithContext returns a copy of ctx carrying params. It is the pure-Go way to
// attach parameters to a context and is useful in tests and in non-Fiber
// rendering paths. In the Fiber render path the framework uses Attach, which
// stores the value on the request context so it survives templ's context
// wrapping.
func WithContext(ctx context.Context, params Params) context.Context {
	return context.WithValue(ctx, key{}, params)
}

// userValueSetter is the minimal value-storage interface that the fasthttp
// request context (and therefore the templ render context) satisfies. Keeping
// this interface here — rather than importing fasthttp — means the public
// package has no dependency on the HTTP server implementation.
type userValueSetter interface {
	SetUserValue(key, value any)
}

// Attach stores params on a request-scoped value store (the fasthttp request
// context) so that FromContext can retrieve it during templ rendering. It is
// the framework-facing entry point called by the internal routing layer. It
// reports false, leaving the store unchanged, when store is nil or does not
// implement the value-storage interface.
func Attach(store any, params Params) bool {
	setter, ok := store.(userValueSetter)
	if !ok {
		return false
	}
	setter.SetUserValue(key{}, params)
	return true
}
