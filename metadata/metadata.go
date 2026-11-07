// Package metadata lets templ pages and layouts set document metadata.
package metadata

import (
	"context"
	"io"

	"github.com/a-h/templ"

	"github.com/akashKarmakar02/meta/internal/metastate"
)

// Page contains metadata for a document. Empty fields inherit the nearest
// templ layout's value, if any.
type Page = metastate.Page

// OpenGraph describes Open Graph fields such as the sharing image.
type OpenGraph = metastate.OpenGraph

// Twitter describes X/Twitter card fields.
type Twitter = metastate.Twitter

// Set records metadata for the current page or layout render. The latest call
// within that render replaces its previous value. Set has no effect when ctx
// does not belong to a Meta page render.
func Set(ctx context.Context, page Page) { metastate.Set(ctx, page) }

// PageComponent marks the page portion of a composed templ document so its
// metadata takes precedence over the layout. Route generation wraps pages with
// this helper; applications normally call Set instead.
func PageComponent(component templ.Component) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return component.Render(metastate.PageContext(ctx), w)
	})
}
