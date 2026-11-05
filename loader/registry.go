package loader

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/akashKarmakar02/meta/routecontext"
)

var (
	ErrInvalidRouteID   = errors.New("loader: invalid route ID")
	ErrUnknownRoute     = errors.New("loader: unknown route ID")
	ErrDuplicateRoute   = errors.New("loader: duplicate route ID")
	ErrDuplicateBinding = errors.New("loader: duplicate binding")
	ErrRouteMismatch    = errors.New("loader: route does not match binding")
)

// Binding associates one declared route ID with a typed loader. Construct it
// using Bind; its zero value is invalid. Binding does not execute the loader.
type Binding struct {
	routeID   string
	invoke    func(context.Context, routecontext.Route) (any, error)
	configErr error
}

// Bind adapts a typed loader to the registry boundary without invoking it.
// NewRegistry validates the route ID and rejects a nil loader.
func Bind[T any](routeID string, load Loader[T]) Binding {
	if load == nil {
		return Binding{routeID: routeID}
	}
	return Binding{routeID: routeID, invoke: func(ctx context.Context, r routecontext.Route) (any, error) { return Execute(ctx, r, load) }}
}

// Registry is an immutable collection of bindings validated against the
// supplied authoritative route IDs. Its zero value has no bindings. Lookup is
// safe for concurrent use; no request-specific state is retained.
type Registry struct {
	known    map[string]struct{}
	bindings map[string]Binding
}

// NewRegistry validates all routes and bindings before returning a registry.
// routeIDs must come from the generated route set. No partial registry is
// returned on error. IDs are source-derived IDs, not concrete URLs.
func NewRegistry(routeIDs []string, bindings ...Binding) (*Registry, error) {
	known := make(map[string]struct{}, len(routeIDs))
	for _, id := range routeIDs {
		if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRouteID, id)
		}
		if _, ok := known[id]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateRoute, id)
		}
		known[id] = struct{}{}
	}
	found := make(map[string]Binding, len(bindings))
	for _, binding := range bindings {
		id := binding.routeID
		if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRouteID, id)
		}
		if _, ok := known[id]; !ok {
			return nil, fmt.Errorf("%w: %q (use a generated source route ID)", ErrUnknownRoute, id)
		}
		if _, ok := found[id]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateBinding, id)
		}
		if binding.configErr != nil {
			return nil, fmt.Errorf("loader: route %q: %w", id, binding.configErr)
		}
		if binding.invoke == nil {
			return nil, fmt.Errorf("%w: route %q", ErrNilLoader, id)
		}
		found[id] = binding
	}
	return &Registry{known: known, bindings: found}, nil
}

// HasRoute reports whether routeID was present in the authoritative route set.
// It distinguishes an unknown ID from a known route without a loader.
func (r *Registry) HasRoute(routeID string) bool {
	if r == nil {
		return false
	}
	_, ok := r.known[routeID]
	return ok
}

// Lookup returns the binding for a declared route ID, or false for a missing
// binding (including unknown IDs). It never executes the loader.
func (r *Registry) Lookup(routeID string) (Registered, bool) {
	if r == nil {
		return Registered{}, false
	}
	b, ok := r.bindings[routeID]
	if !ok {
		return Registered{}, false
	}
	return Registered{binding: b}, true
}

// Registered is an immutable, type-erased loader handle for future dispatch.
// Its zero value is invalid. Typed functions remain checked at Bind's call site.
type Registered struct{ binding Binding }

// Execute explicitly invokes the registered loader, preserving C01 cancellation
// and error behavior. route.RouteID must be the bound declaration. The returned
// value is type-erased only at this boundary; no serialization is performed.
func (r Registered) Execute(ctx context.Context, route routecontext.Route) (any, error) {
	if r.binding.invoke == nil {
		return nil, ErrNilLoader
	}
	if route.RouteID != r.binding.routeID {
		return nil, fmt.Errorf("%w: bound %q, received %q", ErrRouteMismatch, r.binding.routeID, route.RouteID)
	}
	return r.binding.invoke(ctx, route)
}
