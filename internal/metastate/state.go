// Package metastate stores metadata for one render in its request context.
package metastate

import (
	"context"
	"sync"
)

type Page struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type key struct{}
type pageKey struct{}
type state struct {
	mu           sync.Mutex
	layout, page Page
}

func New(ctx context.Context) context.Context         { return context.WithValue(ctx, key{}, &state{}) }
func PageContext(ctx context.Context) context.Context { return context.WithValue(ctx, pageKey{}, true) }
func Set(ctx context.Context, p Page) {
	if s, ok := ctx.Value(key{}).(*state); ok {
		s.mu.Lock()
		if page, _ := ctx.Value(pageKey{}).(bool); page {
			s.page = p
		} else {
			s.layout = p
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
	return layout
}
