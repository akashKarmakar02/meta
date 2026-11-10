// Package sitemap generates and serves a sitemap.xml for Meta applications.
//
// Sitemap exposes routes with static paths by default. Routes with parameters
// are omitted unless the application registers dynamic URLs for them.
// Generated XML is cached: in memory for fast responses and on disk so a
// restart does not force a rebuild before serving. Invalidate clears both
// layers and rebuilds on the next request.
package sitemap

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/metadata"
)

// URL is one sitemap entry. Loc is required; other fields are optional.
type URL struct {
	Loc        string
	LastMod    time.Time
	ChangeFreq string
	Priority   string
}

// RouteSource supplies page routes for the sitemap. Path is the route's
// pattern, such as "/blog" or "/blog/[slug]"; Static reports whether the
// pattern contains no parameters.
type RouteSource interface {
	Routes() []RouteInfo
}

// RouteInfo describes one page route for sitemap generation.
type RouteInfo struct {
	Path string
}

// Source adapts a []RouteInfo to the RouteSource interface.
type Source []RouteInfo

// Routes implements RouteSource.
func (s Source) Routes() []RouteInfo { return []RouteInfo(s) }

// isParameterized reports whether a route path contains dynamic segments
// such as /blog/[slug]. Parameterized routes are only included when the
// developer supplies concrete URLs through Dynamic.
func isParameterized(path string) bool { return strings.Contains(path, "[") }

// DynamicURLs computes the URLs of one parameterized route. Implementations
// typically query the database and return fully resolved locations.
type DynamicURLs func(ctx context.Context) ([]URL, error)

// Config configures a Sitemap.
type Config struct {
	// BaseURL is the site origin, such as "https://example.com". Required.
	BaseURL string

	// Source supplies page routes. Required.
	RouteSource RouteSource

	// Dynamic registers a function that resolves parameterized routes to
	// concrete URLs. Routes with parameters are skipped when no function is
	// registered for their path.
	Dynamic map[string]DynamicURLs

	// Store persists generated XML across restarts. Optional; nil disables
	// persistence.
	Store cache.Store

	// Cache mirrors a metadata.SitemapCache declaration so sites can tune the
	// sitemap cache TTL. Zero TTL caches indefinitely (cache.Never), refreshed
	// only by explicit invalidation.
	Cache metadata.SitemapCache

	// Now overrides the time source in tests. Optional.
	Now func() time.Time
}

// Sitemap generates, caches and serves sitemap XML.
type Sitemap struct {
	cfg   Config
	now   func() time.Time
	mu    sync.Mutex // guards xml + genAt
	xml   []byte     // last rendered XML; nil until first build
	genAt time.Time  // when xml was rendered
}

// New creates a Sitemap. It returns an error when BaseURL or RouteSource is
// missing or the BaseURL is not an absolute http(s) origin.
func New(cfg Config) (*Sitemap, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("sitemap: baseURL required")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" ||
		u.User != nil || u.Opaque != "" || u.Fragment != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("sitemap: baseURL %q must be an absolute http(s) origin", cfg.BaseURL)
	}
	if cfg.RouteSource == nil {
		return nil, fmt.Errorf("sitemap: route source required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Sitemap{cfg: cfg, now: now}, nil
}

// cacheKey is the sitemap's identity in a persistent store. It is fixed: a
// sitemap reflects site content, not per-request data.
func (s *Sitemap) cacheKey() (cache.Key, error) {
	return cache.NewKey("meta", "sitemap")
}

// persistPolicy derives the sitemap cache policy. With no metadata.SitemapCache
// declaration (TTL zero) entries are cached indefinitely and refreshed only by
// explicit invalidation; a declared TTL bounds freshness instead.
func (s *Sitemap) persistPolicy() cache.Policy {
	ttl := s.cfg.Cache.TTLOrDefault()
	if ttl <= 0 {
		return cache.Policy{TTL: cache.Never}
	}
	return cache.Policy{TTL: ttl}
}

// XML returns the sitemap XML, generating it on first use. Concurrent calls
// during the initial build deduplicate to a single generation pass.
func (s *Sitemap) XML(ctx context.Context) ([]byte, error) {
	if cached, ok := s.tryXML(ctx); ok {
		return cached, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.xml != nil { // built while we waited on the lock
		return s.xml, nil
	}
	urls, err := s.collect(ctx)
	if err != nil {
		return nil, err
	}
	xml, err := render(urls)
	if err != nil {
		return nil, err
	}
	s.xml = xml
	s.genAt = s.now()
	if s.cfg.Store != nil {
		// Best effort: persistence is an optimization, not a correctness gate.
		if key, kerr := s.cacheKey(); kerr == nil {
			if serr := s.cfg.Store.Set(ctx, key, cache.Entry{Value: xml}, s.persistPolicy()); serr != nil {
				_ = serr // best effort
			}
		}
	}
	return xml, nil
}

// tryXML serves from memory or the persistent store without blocking other
// readers. ok is false when a build is required.
func (s *Sitemap) tryXML(ctx context.Context) (xml []byte, ok bool) {
	s.mu.Lock()
	if s.xml != nil {
		defer s.mu.Unlock()
		return s.xml, true
	}
	s.mu.Unlock()
	if s.cfg.Store != nil {
		if key, kerr := s.cacheKey(); kerr == nil {
			if entry, hit, gerr := s.cfg.Store.Get(ctx, key); gerr == nil && hit && len(entry.Value) > 0 {
				return entry.Value, true
			}
		}
	}
	return nil, false
}

// Invalidate clears the in-memory copy and the persisted XML. The next XML
// call regenerates from current routes.
func (s *Sitemap) Invalidate(ctx context.Context) {
	s.mu.Lock()
	s.xml = nil
	s.genAt = time.Time{}
	s.mu.Unlock()
	if s.cfg.Store != nil {
		if key, kerr := s.cacheKey(); kerr == nil {
			_ = s.cfg.Store.Delete(ctx, key)
		}
	}
}

// collect walks the route source, expands dynamic routes and returns the
// sorted final URL list.
func (s *Sitemap) collect(ctx context.Context) ([]URL, error) {
	routes := s.cfg.RouteSource.Routes()
	urls := make([]URL, 0, len(routes))
	for _, r := range routes {
		if !isParameterized(r.Path) {
			u, err := s.locFor(r.Path)
			if err != nil {
				return nil, err
			}
			urls = append(urls, URL{Loc: u})
			continue
		}
		fn, ok := s.cfg.Dynamic[r.Path]
		if !ok {
			continue // parameterized route without a resolver is omitted
		}
		resolved, err := fn(ctx)
		if err != nil {
			return nil, fmt.Errorf("sitemap: dynamic route %s: %w", r.Path, err)
		}
		for _, du := range resolved {
			loc, err := s.locFor(du.Loc)
			if err != nil {
				return nil, err
			}
			u := URL{Loc: loc, LastMod: du.LastMod, ChangeFreq: du.ChangeFreq, Priority: du.Priority}
			urls = append(urls, u)
		}
	}
	sort.SliceStable(urls, func(i, j int) bool { return urls[i].Loc < urls[j].Loc })
	return urls, nil
}

// locFor joins the base origin with a site path and validates the result.
func (s *Sitemap) locFor(path string) (string, error) {
	if path == "" || path[0] != '/' {
		return "", fmt.Errorf("sitemap: route path %q must start with /", path)
	}
	u, err := url.Parse(s.cfg.BaseURL + path)
	if err != nil {
		return "", fmt.Errorf("sitemap: invalid loc for %s: %w", path, err)
	}
	if u.Fragment != "" || u.RawQuery != "" {
		return "", fmt.Errorf("sitemap: loc %q must not contain query or fragment", u.String())
	}
	return u.String(), nil
}

// render serializes URLs to sitemap XML.
func render(urls []URL) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		b.WriteString("  <url>\n    <loc>")
		if err := xmlText(&b, u.Loc); err != nil {
			return nil, err
		}
		b.WriteString("</loc>\n")
		if !u.LastMod.IsZero() {
			fmt.Fprintf(&b, "    <lastmod>%s</lastmod>\n", u.LastMod.UTC().Format("2006-01-02"))
		}
		if u.ChangeFreq != "" {
			b.WriteString("    <changefreq>")
			if err := xmlText(&b, u.ChangeFreq); err != nil {
				return nil, err
			}
			b.WriteString("</changefreq>\n")
		}
		if u.Priority != "" {
			b.WriteString("    <priority>")
			if err := xmlText(&b, u.Priority); err != nil {
				return nil, err
			}
			b.WriteString("</priority>\n")
		}
		b.WriteString("  </url>\n")
	}
	b.WriteString("</urlset>\n")
	return []byte(b.String()), nil
}

// xmlText writes escaped text content.
func xmlText(b *strings.Builder, s string) error {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\'':
			b.WriteString("&apos;")
		case '"':
			b.WriteString("&quot;")
		default:
			if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
				return fmt.Errorf("sitemap: control character in loc")
			}
			b.WriteRune(r)
		}
	}
	return nil
}
