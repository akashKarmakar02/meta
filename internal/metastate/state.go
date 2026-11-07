// Package metastate stores metadata for one render in its request context.
package metastate

import (
	"context"
	"net/url"
	"strings"
	"sync"
)

// OpenGraph describes the fields used by social sharing platforms.
type OpenGraph struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	URL         string `json:"url,omitempty"`
	Type        string `json:"type,omitempty"`
	SiteName    string `json:"siteName,omitempty"`
}

// Twitter describes X/Twitter card fields.
type Twitter struct {
	Card        string `json:"card,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	Site        string `json:"site,omitempty"`
}

type Page struct {
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	Canonical   string    `json:"canonical,omitempty"`
	OpenGraph   OpenGraph `json:"openGraph,omitempty,omitzero"`
	Twitter     Twitter   `json:"twitter,omitempty,omitzero"`
}

type key struct{}
type pageKey struct{}
type state struct {
	mu           sync.Mutex
	layout, page Page
}

func New(ctx context.Context) context.Context         { return context.WithValue(ctx, key{}, &state{}) }
func PageContext(ctx context.Context) context.Context { return context.WithValue(ctx, pageKey{}, true) }

// SafeURL limits metadata URLs to absolute HTTP(S) or root-relative URLs.
// Network-path references, userinfo and control characters are not accepted.
func SafeURL(value string) string {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\\\r\n\t") {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Opaque != "" {
		return ""
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && u.Host == "" {
		return value
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" {
		return value
	}
	return ""
}

func clean(p Page) Page {
	p.Canonical = SafeURL(p.Canonical)
	p.OpenGraph.Image = SafeURL(p.OpenGraph.Image)
	p.OpenGraph.URL = SafeURL(p.OpenGraph.URL)
	p.Twitter.Image = SafeURL(p.Twitter.Image)
	return p
}

func Set(ctx context.Context, p Page) {
	if s, ok := ctx.Value(key{}).(*state); ok {
		s.mu.Lock()
		if page, _ := ctx.Value(pageKey{}).(bool); page {
			s.page = clean(p)
		} else {
			s.layout = clean(p)
		}
		s.mu.Unlock()
	}
}
func Get(ctx context.Context) Page {
	if s, ok := ctx.Value(key{}).(*state); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return Merge(s.layout, s.page)
	}
	return Page{}
}
func Merge(layout, page Page) Page {
	if page.Title != "" {
		layout.Title = page.Title
	}
	if page.Description != "" {
		layout.Description = page.Description
	}
	if page.Canonical != "" {
		layout.Canonical = page.Canonical
	}
	if page.OpenGraph.Title != "" {
		layout.OpenGraph.Title = page.OpenGraph.Title
	}
	if page.OpenGraph.Description != "" {
		layout.OpenGraph.Description = page.OpenGraph.Description
	}
	if page.OpenGraph.Image != "" {
		layout.OpenGraph.Image = page.OpenGraph.Image
	}
	if page.OpenGraph.URL != "" {
		layout.OpenGraph.URL = page.OpenGraph.URL
	}
	if page.OpenGraph.Type != "" {
		layout.OpenGraph.Type = page.OpenGraph.Type
	}
	if page.OpenGraph.SiteName != "" {
		layout.OpenGraph.SiteName = page.OpenGraph.SiteName
	}
	if page.Twitter.Card != "" {
		layout.Twitter.Card = page.Twitter.Card
	}
	if page.Twitter.Title != "" {
		layout.Twitter.Title = page.Twitter.Title
	}
	if page.Twitter.Description != "" {
		layout.Twitter.Description = page.Twitter.Description
	}
	if page.Twitter.Image != "" {
		layout.Twitter.Image = page.Twitter.Image
	}
	if page.Twitter.Site != "" {
		layout.Twitter.Site = page.Twitter.Site
	}
	return layout
}
