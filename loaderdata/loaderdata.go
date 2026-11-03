// Package loaderdata delivers a server-side loader's typed result to templ
// components during SSR rendering.
//
// The framework executes a route's registered loader exactly once per matched
// SSR request (C03) and attaches the result to the request-lifetime render
// context. A templ page or layout reads it back with a strong type:
//
//	post, ok := loaderdata.Get[PostData](ctx)
//	if !ok {
//		// render a fallback; loaderdata.Has(ctx) says whether any loader ran.
//	}
//
// The package imports only the standard library. It never imports Fiber or
// fasthttp and never exposes a request object: the framework-facing entry
// point (WithContext) takes the already-erased value produced by the loader
// registry, and application code only ever calls Get and Has.
package loaderdata

import "context"

// key is the private, collision-resistant context key under which a loader
// result is stored. Application code must not construct its own key or look
// up loader data by an arbitrary key; it must use Get and Has.
type key struct{}

// WithContext returns a copy of ctx carrying value, the loader result for
// this request. It is the framework-facing attachment point (C03 wires it
// into the render context after a successful loader execution). Application
// code should obtain data from Get, not by calling WithContext.
func WithContext(ctx context.Context, value any) context.Context {
	return context.WithValue(ctx, key{}, value)
}

// Get returns the loader result attached to ctx, if it is exactly of type T.
// ok is true only when a result whose dynamic type is T is present. A zero
// value of T is a valid result and still reports ok=true; it is therefore
// distinguishable from the absence of any loader data (ok=false).
//
// Get never panics. A request for a type different from the one the loader
// returned reports ok=false; use Has to tell that case apart from "no loader
// ran at all".
//
// In a templ component, ctx is the framework's request-lifetime render
// context, so this is the standard way to read loader data:
//
//	post, ok := loaderdata.Get[PostData](ctx)
func Get[T any](ctx context.Context) (T, bool) {
	var zero T
	if ctx == nil {
		return zero, false
	}
	val := ctx.Value(key{})
	v, ok := val.(T)
	return v, ok
}

// Has reports whether any loader result is attached to ctx, regardless of its
// type. It is false when no loader ran for the request (the route has no
// registered loader, or the request is not an SSR render). It is true when a
// loader ran and attached a result, even if Get for a different type reports
// ok=false.
func Has(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	return ctx.Value(key{}) != nil
}
