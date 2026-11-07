// Package requeststate carries application-defined values through one request.
package requeststate

import (
	"context"

	"github.com/gofiber/fiber/v3"
)

// Key identifies a request-local value. Create each key once as a package variable.
// Keys are unique by identity, even when they have the same value type.
type Key[T any] struct{ id *byte }

// NewKey creates a unique key for values of type T.
func NewKey[T any]() Key[T] { return Key[T]{id: new(byte)} }

// Set stores value in the Fiber request context for downstream handlers,
// loaders, actions and templ rendering. A zero Key must not be used.
func Set[T any](c fiber.Ctx, key Key[T], value T) {
	if key.id == nil {
		panic("requeststate: use NewKey to create a key")
	}
	c.SetContext(context.WithValue(c.Context(), key, value))
}

// Get retrieves a value set earlier in the same request.
func Get[T any](ctx context.Context, key Key[T]) (T, bool) {
	var zero T
	if ctx == nil || key.id == nil {
		return zero, false
	}
	value, ok := ctx.Value(key).(T)
	return value, ok
}
