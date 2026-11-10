package metadata

import "time"

// SitemapCache controls how the generated sitemap.xml is cached.
//
// Declare it once per site (a page.templ file is a natural home, since templ
// passes package-level declarations through to the generated Go file):
//
//	var SitemapCache = metadata.SitemapCache{
//		TTL: 24 * time.Hour,
//	}
//
// TTL bounds how long a generated sitemap.xml stays cached. The zero value
// caches with cache.Never, meaning entries live until explicitly invalidated
// through sitemap.Invalidate or the site's cache store is cleared.
type SitemapCache struct {
	// TTL is how long the sitemap XML stays cached before regeneration.
	// Use time.Duration values. Zero means "cache indefinitely".
	TTL time.Duration
}

// DefaultSitemapCacheTTL is the TTL used when a SitemapCache declaration is
// absent: cache indefinitely (until explicit invalidation). The zero value of
// SitemapCache means the same thing.
const DefaultSitemapCacheTTL = 0

// TTLOrDefault converts the declaration into a cache TTL, falling back to
// cache.Never semantics (indefinite caching) when unset.
func (sc SitemapCache) TTLOrDefault() time.Duration {
	if sc.TTL <= 0 {
		return DefaultSitemapCacheTTL
	}
	return sc.TTL
}
