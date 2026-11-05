// Package routecontext exposes immutable metadata for a matched page request.
// Values returned by FromContext remain safe to read after request completion.
package routecontext

import (
	"context"
	"net/url"

	"github.com/akashKarmakar02/meta/routeparams"
)

type contextKey struct{}

// Route describes a matched route. Its zero value has empty identity, no
// parameters, and no query values. Params and Query expose defensive views.
type Route struct {
	RouteID string
	Pattern string
	Params  routeparams.Params
	Query   Query
}

// Query is a read-only view of decoded query parameters. Its zero value is empty.
type Query struct{ values url.Values }

// NewQuery makes an independent snapshot of values.
func NewQuery(values url.Values) Query {
	copied := make(url.Values, len(values))
	for key, entries := range values {
		copied[key] = append([]string(nil), entries...)
	}
	return Query{values: copied}
}

// Get returns the first value for key, or an empty string when absent.
func (q Query) Get(key string) string { return q.values.Get(key) }

// Values returns a copy of the values for key, or nil when absent.
func (q Query) Values(key string) []string { return append([]string(nil), q.values[key]...) }

// Has reports whether key is present, including a key with an empty value.
func (q Query) Has(key string) bool { return q.values.Has(key) }

// All returns a deep copy of the query values.
func (q Query) All() url.Values {
	result := make(url.Values, len(q.values))
	for key, entries := range q.values {
		result[key] = append([]string(nil), entries...)
	}
	return result
}

// FromContext returns the matched route, or false when no route is attached.
func FromContext(ctx context.Context) (Route, bool) {
	if ctx == nil {
		return Route{}, false
	}
	value, ok := ctx.Value(contextKey{}).(Route)
	return value, ok
}

// WithContext returns a context carrying route metadata.
func WithContext(ctx context.Context, route Route) context.Context {
	return context.WithValue(ctx, contextKey{}, route)
}
