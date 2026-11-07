// Package render builds HTML responses for the go-meta framework: full
// documents for SSR pages and client-mount shells for CSR pages.
package render

import (
	"context"
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/a-h/templ"

	"github.com/gofiber/fiber/v3"

	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/metastate"
	"github.com/akashKarmakar02/meta/internal/routing"
)

// VisualLayoutResolver applies the selected server-owned visual layout.
type VisualLayoutResolver func(routing.Route, templ.Component) (templ.Component, error)

func document(content, tags, title, dataScript string) string {
	return documentWithMetadata(content, tags, title, dataScript, metastate.Page{})
}

func documentWithMetadata(content, tags, title, dataScript string, meta metastate.Page) string {
	var head strings.Builder
	metaTag := func(attribute, name, value string) {
		if value == "" {
			return
		}
		head.WriteString(`<meta ` + attribute + `="` + name + `" data-meta-managed="` + name + `" content="` + html.EscapeString(value) + `">`)
	}
	metaTag("name", "description", meta.Description)
	if meta.Canonical != "" {
		head.WriteString(`<link rel="canonical" data-meta-managed="canonical" href="` + html.EscapeString(meta.Canonical) + `">`)
	}
	metaTag("property", "og:title", meta.OpenGraph.Title)
	metaTag("property", "og:description", meta.OpenGraph.Description)
	metaTag("property", "og:image", meta.OpenGraph.Image)
	metaTag("property", "og:url", meta.OpenGraph.URL)
	metaTag("property", "og:type", meta.OpenGraph.Type)
	metaTag("property", "og:site_name", meta.OpenGraph.SiteName)
	metaTag("name", "twitter:card", meta.Twitter.Card)
	metaTag("name", "twitter:title", meta.Twitter.Title)
	metaTag("name", "twitter:description", meta.Twitter.Description)
	metaTag("name", "twitter:image", meta.Twitter.Image)
	metaTag("name", "twitter:site", meta.Twitter.Site)
	styles, script := tags, ""
	if i := strings.Index(tags, "<script"); i >= 0 {
		styles, script = tags[:i], tags[i:]
	}
	return "<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\">" +
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>" +
		html.EscapeString(title) + "</title>" + head.String() + styles + "</head><body><div data-go-page-outlet>" +
		content + "</div>" + dataScript + script + "</body></html>"
}

func csrMountMarkup(route routing.Route) string {
	id := html.EscapeString(route.ID)
	path := html.EscapeString(route.Path)
	return "<div id=\"" + MountID + "\" " + PageRootAttr + " " + RouteAttr + "=\"" + path + "\" " + RouteIDAttr + "=\"" + id + "\"></div>"
}

func csrVisual(ctx context.Context, route routing.Route, resolve VisualLayoutResolver, requestedURL string) (string, error) {
	mountRoute := route
	mountRoute.Path = requestedURL
	markup := csrMountMarkup(mountRoute)
	if resolve == nil {
		if len(route.Layouts) > 0 && route.Layouts[0].Mode == routing.ModeSSR {
			return "", fmt.Errorf("missing templ visual-layout resolver for route %q", route.ID)
		}
		return markup, nil
	}
	component := templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, markup)
		return err
	})
	wrapped, err := resolve(route, component)
	if err != nil {
		return "", fmt.Errorf("resolve visual layout: %w", err)
	}
	data, err := RenderComponent(ctx, wrapped)
	return string(data), err
}

// Shell renders the minimal HTML5 document served for CSR (page.svelte)
// routes. The browser receives this shell on direct navigation or reload;
// Phase E's client runtime boots inside MountID using RouteAttr to find
// the requested page. The shape of this document is a stable contract:
// later phases add script/style tags (Vite assets) without changing the
// mount element or metadata attributes.
//
// E08: when assets are supplied, the Vite entry module script (and entry
// CSS, if any) is injected from the production manifest. Hashed names
// come from the manifest via httpserver.Assets — never hardcoded. The
// E09 static middleware serves them under the same /assets prefix.
func Shell(route routing.Route, assets *httpserver.Assets) string {
	return document(csrMountMarkup(route), entryTags(assets), route.ID, "")
}

// ShellHandler returns a Fiber handler that responds with the rendered
// shell for a fixed route — keeping HTTP details out of the renderer.
// A nil assets value renders the bare mount-point shell (used by tests;
// production wiring supplies manifest-resolved assets).
func ShellHandler(route routing.Route, assets *httpserver.Assets) func(c fiber.Ctx) error {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.SendString(Shell(route, assets))
	}
}

// MountID is the DOM element the Svelte runtime mounts into.
const MountID = "app"

// PageRootAttr marks the shell's mount point explicitly (blueprint §4:
// the client runtime queries [data-go-page-root] to find the app root).
const PageRootAttr = "data-go-page-root"

// RouteAttr and RouteIDAttr are the stable data attributes the client
// runtime reads to identify the requested page.
const (
	RouteAttr   = "data-go-route"
	RouteIDAttr = "data-go-route-id"
)
