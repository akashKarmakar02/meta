// Package pagecache provides opt-in eager dual-representation SSR caching.
package pagecache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/ssrresponse"
	"github.com/akashKarmakar02/meta/observability"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/gofiber/fiber/v3"
)

var ErrPageCacheDisabled = errors.New("pagecache: page cache disabled")

// GeneratedPage contains one coherent generation without request-specific headers.
type GeneratedPage = ssrresponse.Page
type CachedResponse = ssrresponse.Response
type Generator func(fiber.Ctx) (*GeneratedPage, error)
type Option func(*PageCache)

// PublicRoutes declares deterministic, anonymous route IDs safe for shared
// caching. Omission disables caching. Never opt in personalized pages, including
// pages reading identity/tenant/private headers through a Go context.
func PublicRoutes(ids ...string) Option {
	return func(pc *PageCache) {
		for _, id := range ids {
			pc.public[id] = true
		}
	}
}

// BuildID scopes persisted pages to an application deployment. Change it when
// templates, loaders, layout selection, or assets change.
func BuildID(id string) Option {
	return func(pc *PageCache) {
		if id != "" {
			pc.buildID = id
		}
	}
}

// GenerationTimeout bounds synchronous work. Loaders/components must cooperate
// with context cancellation. No Fiber object outlives its request handler.
func GenerationTimeout(timeout time.Duration) Option {
	return func(pc *PageCache) {
		if timeout > 0 {
			pc.timeout = timeout
		}
	}
}

// PageCacheConfig is retained for source compatibility. Construct with
// NewPageCache; stale responses are not served by this cache.
type PageCacheConfig struct {
	Store                cache.Store
	TTL                  time.Duration
	StaleWhileRevalidate time.Duration
}

// PageCache coordinates one store in one active process. Sequential restarts
// retain invalidation tokens. Concurrent processes sharing a disk directory
// require external coordination and are unsupported by the Store contract.
type PageCache struct {
	store   cache.Store
	ttl     time.Duration
	timeout time.Duration
	buildID string
	public  map[string]bool
	// mu orders token checks, writes and invalidation; never loader/render work.
	mu      sync.Mutex
	flights map[cache.Key]*flight
}

type flight struct {
	done chan struct{}
	page *GeneratedPage
	err  error
}

func NewPageCache(store cache.Store, ttl time.Duration, options ...Option) *PageCache {
	pc := &PageCache{store: store, ttl: ttl, timeout: 30 * time.Second, buildID: executableBuildID(), public: make(map[string]bool), flights: make(map[cache.Key]*flight)}
	for _, option := range options {
		option(pc)
	}
	return pc
}

// Wrap integrates a deliberate pair generator after required middleware and
// before loaders. next is the uncached path. Without a generator it passes
// through: a single HTTP response cannot establish a coherent pair.
// The owning request generates synchronously with a bounded context; waiters
// share only immutable bytes and cancel independently. No background worker
// or Fiber request object survives a handler's return.
func (pc *PageCache) Wrap(next fiber.Handler, generators ...Generator) fiber.Handler {
	if pc == nil || pc.store == nil || len(generators) != 1 || generators[0] == nil {
		return next
	}
	generate := generators[0]
	return func(c fiber.Ctx) error {
		route, ok := routecontext.FromContext(c.Context())
		if !ok || !pc.public[route.RouteID] || !eligible(c) {
			return next(c)
		}
		path := normalizePath(string(c.Request().URI().PathOriginal()))
		// The envelope preserves URL spelling and query order. An equivalent
		// decoded query must not receive the first caller's differently spelled URL.
		rawURL := string(c.Request().URI().PathOriginal())
		if query := string(c.Request().URI().QueryString()); query != "" {
			rawURL += "?" + query
		}
		ctx := c.Context()
		for {
			pc.mu.Lock()
			token, err := pc.pathToken(ctx, path)
			if err != nil {
				pc.mu.Unlock()
				pc.record(ctx, route.RouteID, "lookup", "error")
				return next(c)
			}
			key, err := pc.pageKey(route, rawURL, token)
			if err != nil {
				pc.mu.Unlock()
				return next(c)
			}
			entry, hit, err := pc.store.Get(ctx, key)
			if err != nil {
				pc.mu.Unlock()
				pc.record(ctx, route.RouteID, "lookup", "error")
				return next(c)
			}
			if hit {
				if page, err := decodePage(entry.Value); err == nil {
					pc.mu.Unlock()
					pc.record(ctx, route.RouteID, "dual", "hit")
					return send(c, page)
				}
			}
			pc.record(ctx, route.RouteID, "dual", "miss")
			if f := pc.flights[key]; f != nil {
				pc.mu.Unlock()
				pc.record(ctx, route.RouteID, "generation", "coalesced")
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-f.done:
				}
				if f.err != nil {
					return f.err
				}
				if f.page != nil {
					return send(c, f.page)
				}
				// Unsafe output, canceled owner or invalidation: retry using this
				// request. Never replay cookies, redirects or personalized output.
				continue
			}
			f := &flight{done: make(chan struct{})}
			pc.flights[key] = f
			pc.mu.Unlock()
			return pc.fill(c, route, path, token, key, f, generate)
		}
	}
}

func (pc *PageCache) fill(c fiber.Ctx, route routecontext.Route, path, token string, key cache.Key, f *flight, generate Generator) error {
	// Fiber recovery handles panics; always release waiters, including on panic.
	defer func() {
		pc.mu.Lock()
		delete(pc.flights, key)
		close(f.done)
		pc.mu.Unlock()
	}()
	parent := c.Context()
	ctx, cancel := context.WithTimeout(parent, pc.timeout)
	defer cancel()
	c.SetContext(ctx)
	defer c.SetContext(parent)
	pc.record(ctx, route.RouteID, "generation", "started")
	page, err := generate(c)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		pc.record(ctx, route.RouteID, "generation", "failed")
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			f.err = err
		}
		return err
	}
	if page == nil { // loader redirect/control-flow already wrote the response
		pc.record(ctx, route.RouteID, "generation", "failed")
		return nil
	}
	pc.record(ctx, route.RouteID, "generation", "completed")
	if eligible(c) && validPage(page) {
		value, encodeErr := encodePage(page)
		if encodeErr == nil {
			pc.mu.Lock()
			current, tokenErr := pc.pathToken(ctx, path)
			if tokenErr == nil && current == token {
				f.page = page
				if err := pc.store.Set(ctx, key, cache.Entry{Value: value}, cache.Policy{TTL: pc.ttl}); err != nil {
					pc.record(ctx, route.RouteID, "write", "failed")
				}
			} else {
				pc.record(ctx, route.RouteID, "generation", "stale-rejected")
			}
			pc.mu.Unlock()
		}
	}
	return send(c, page)
}

func eligible(c fiber.Ctx) bool {
	if c.Method() != fiber.MethodGet {
		return false
	}
	// Check presence across every line, including an empty first line followed
	// by credentials interpreted by application middleware.
	for _, name := range []string{fiber.HeaderCookie, fiber.HeaderAuthorization, "Proxy-Authorization"} {
		if len(c.Request().Header.PeekAll(name)) != 0 {
			return false
		}
	}
	if c.Response().StatusCode() != fiber.StatusOK || len(c.Response().Header.PeekAll(fiber.HeaderSetCookie)) != 0 {
		return false
	}
	for _, value := range c.Request().Header.PeekAll(fiber.HeaderCacheControl) {
		if forbiddenControl(string(value)) {
			return false
		}
	}
	for _, value := range c.Response().Header.PeekAll(fiber.HeaderCacheControl) {
		if forbiddenControl(string(value)) {
			return false
		}
	}
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		for _, value := range c.Response().Header.PeekAll(name) {
			if strings.Contains(strings.ToLower(string(value)), "'nonce-") {
				return false
			}
		}
	}
	return len(c.Response().Header.PeekAll(fiber.HeaderVary)) == 0
}

func forbiddenControl(value string) bool {
	for _, directive := range strings.Split(strings.ToLower(value), ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(directive), "=")
		name = strings.TrimSpace(name)
		if name == "private" || name == "no-store" {
			return true
		}
	}
	return false
}

func send(c fiber.Ctx, page *GeneratedPage) error {
	response := page.HTML
	navigation := c.Get(httpserver.NavigationHeader) == "1"
	if navigation {
		response = page.Navigation
	}
	if response.CacheControl != "" {
		c.Set(fiber.HeaderCacheControl, response.CacheControl)
	}
	if response.ContentLanguage != "" {
		c.Set(fiber.HeaderContentLanguage, response.ContentLanguage)
	}
	// Protocol no-store follows internal validation. Application no-store was
	// checked before lookup and before writing; no other header can override it.
	if navigation {
		c.Set(fiber.HeaderCacheControl, "no-store")
	}
	c.Set(fiber.HeaderContentType, response.ContentType)
	c.Status(response.StatusCode)
	// Fiber Send borrows the slice. Downstream middleware may mutate its
	// response body after c.Next(), so give each response its own buffer while
	// the immutable flight bytes remain safe for concurrent waiters.
	c.Response().SetBody(response.Body)
	return nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (pc *PageCache) tokenKey(path string) cache.Key {
	key, _ := cache.NewKey("ssr-path:v1", digest(pc.buildID), digest(path))
	return key
}

// Called with mu held. Missing/evicted tokens get fresh randomness; a default
// token could resurrect old pages when a metadata entry is evicted.
func (pc *PageCache) pathToken(ctx context.Context, path string) (string, error) {
	entry, hit, err := pc.store.Get(ctx, pc.tokenKey(path))
	if err != nil {
		return "", err
	}
	if hit && len(entry.Value) == 32 {
		return string(entry.Value), nil
	}
	return pc.advanceToken(ctx, path)
}

func (pc *PageCache) advanceToken(ctx context.Context, path string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(random[:])
	err := pc.store.Set(ctx, pc.tokenKey(path), cache.Entry{Value: []byte(token)}, cache.Policy{TTL: cache.Never})
	return token, err
}

func (pc *PageCache) pageKey(route routecontext.Route, rawURL, token string) (cache.Key, error) {
	return cache.NewKey("ssr-page:v3", digest(pc.buildID), digest(route.RouteID+"\x00"+route.Pattern), digest(rawURL), token)
}

// Invalidate invalidates all query variants of a concrete route. Pattern uses
// declaration syntax (/blog/[slug]); Params supplies decoded parameter values.
func (pc *PageCache) Invalidate(ctx context.Context, route routecontext.Route) error {
	path := route.Pattern
	for name, value := range route.Params.All() {
		path = strings.ReplaceAll(path, "["+name+"]", url.PathEscape(value))
		segments := strings.Split(value, "/")
		for i := range segments {
			segments[i] = url.PathEscape(segments[i])
		}
		path = strings.ReplaceAll(path, "[..."+name+"]", strings.Join(segments, "/"))
	}
	return pc.InvalidatePath(ctx, path)
}

// InvalidatePath replaces a durable path token, invalidating the entire pair
// and all query variants even after restart. Older fills cannot publish into
// the new generation. Old requests may finish with their original snapshot.
// No scan is needed; unreachable entries expire by TTL (Never needs offline
// reclamation). Failed token writes promise no invalidation and return an error.
func (pc *PageCache) InvalidatePath(ctx context.Context, path string) error {
	if pc == nil || pc.store == nil {
		return ErrPageCacheDisabled
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return errors.New("pagecache: expected concrete absolute path without query or fragment")
	}
	pc.mu.Lock()
	_, err := pc.advanceToken(ctx, normalizePath(path))
	pc.mu.Unlock()
	outcome := "invalidated"
	if err != nil {
		outcome = "error"
	}
	pc.record(ctx, "pagecache", "cache", outcome)
	return err
}

func normalizePath(value string) string {
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	return path.Clean(value)
}

func (pc *PageCache) record(ctx context.Context, route, operation, outcome string) {
	observability.Record(ctx, "pagecache", route, operation, outcome, 0)
}
