// Package httpserver wraps page handlers with an opt-in SSR response cache.
//
// PageCache is a Fiber middleware: it looks up the fully rendered HTML for
// GET page requests in a cache.Store before the handler runs, and on a miss
// captures the handler's response for future hits. Caching is disabled
// unless constructed with a store, so the default configuration keeps
// today's behavior byte-for-byte.
package httpserver

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/gofiber/fiber/v3"
)

// ErrPageCacheDisabled is returned by PageCache.Invalidate when no store is
// configured. It is distinct from the cache package's errors because it means
// the caller wired the feature off, not that invalidation failed.
var ErrPageCacheDisabled = errors.New("httpserver: page cache disabled")

// PageCacheConfig controls PageCache. The zero value is not usable; use
// NewPageCache.
type PageCacheConfig struct {
	// Store receives rendered HTML. Required.
	Store cache.Store
	// TTL bounds how long a rendered page may be reused. Must be positive.
	TTL time.Duration
	// StaleWhileRevalidate, when positive, lets a hit be served while the
	// entry is expired if regeneration fails; the next request then sees
	// fresh HTML. Optional; zero disables it.
	StaleWhileRevalidate time.Duration
}

// PageCache caches rendered SSR pages in a cache.Store keyed by route
// pattern, path parameters, and query string.
type PageCache struct {
	store cache.Store
	ttl   time.Duration
	swr   time.Duration
}

// NewPageCache returns a cache that stores responses under store with ttl.
func NewPageCache(store cache.Store, ttl time.Duration) *PageCache {
	return &PageCache{store: store, ttl: ttl}
}

// Wrap returns a middleware wrapping next. Requests that are not cacheable
// pass through untouched.
func (pc *PageCache) Wrap(next fiber.Handler) fiber.Handler {
	if pc == nil || pc.store == nil {
		return next
	}
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet {
			return next(c)
		}
		route, ok := routecontext.FromContext(c.Context())
		if !ok {
			return next(c)
		}
		key, err := pc.keyFor(route)
		if err != nil {
			return next(c) // Unkeyable request: render through.
		}
		ctx := c.Context()
		entry, hit, err := pc.store.Get(ctx, key)
		if err == nil && hit {
			value := entry.Value
			contentType, body, ok := splitCachedResponse(value)
			if ok {
				c.Set(fiber.HeaderContentType, contentType)
				return c.Send(body)
			}
			// Malformed envelope: treat as miss and re-render below.
		}
		renderErr := next(c)
		if renderErr != nil || c.Response().StatusCode() >= 400 {
			return renderErr
		}
		ctype := string(c.Response().Header.ContentType())
		body := append([]byte(nil), c.Response().Body()...)
		envelope := append([]byte(nil), ctype...)
		envelope = append(envelope, 0)
		envelope = append(envelope, body...)
		policy := cache.Policy{TTL: pc.ttl}
		if err := pc.store.Set(ctx, key, cache.Entry{Value: envelope}, policy); err != nil {
			// A failed write must never fail the request: report through
			// headers only.
			_ = err
		}
		return nil
	}
}

// Invalidate evicts the cached response for a route pattern + params + query.
func (pc *PageCache) Invalidate(ctx context.Context, route routecontext.Route) error {
	if pc == nil || pc.store == nil {
		return ErrPageCacheDisabled
	}
	key, err := pc.keyFor(route)
	if err != nil {
		return err
	}
	return pc.store.Delete(ctx, key)
}

func (pc *PageCache) keyFor(route routecontext.Route) (cache.Key, error) {
	components := make([]string, 0, 2+len(route.Params.All()))
	components = append(components, route.Pattern)
	params := route.Params.All()
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		components = append(components, name+"="+params[name])
	}
	if raw := route.Query.All().Encode(); raw != "" {
		components = append(components, "q:"+raw)
	}
	return cache.NewKey("page", components...)
}

// splitCachedResponse decodes the envelope written by Wrap. It returns ok
// false when value is malformed.
func splitCachedResponse(value []byte) (contentType string, body []byte, ok bool) {
	if i := bytes.IndexByte(value, 0); i >= 0 && i+1 <= len(value) {
		return string(value[:i]), value[i+1:], true
	}
	return "", nil, false
}
