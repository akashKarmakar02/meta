// Package render builds HTML responses for the go-meta framework: full
// documents for SSR pages and client-mount shells for CSR pages.
package render

import (
	"html"

	"github.com/gofiber/fiber/v3"

	"go-meta/internal/httpserver"
	"go-meta/internal/routing"
)

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
	// Route ID and path come from our own generated manifest (already
	// validated, slash-normalized paths), but escape anyway so no route
	// naming choice can ever inject markup into the document.
	id := html.EscapeString(route.ID)
	path := html.EscapeString(route.Path)

	head := "<!DOCTYPE html>\n" +
		"<html lang=\"en\">\n" +
		"<head>\n" +
		"<meta charset=\"utf-8\">\n" +
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n" +
		"<title>" + id + "</title>\n"
	if assets != nil {
		for _, css := range assets.EntryCSSURLs {
			head += "<link rel=\"stylesheet\" href=\"" + html.EscapeString(css) + "\">\n"
		}
	}
	head += "</head>\n" +
		"<body>\n" +
		"<div id=\"app\" data-go-route=\"" + path + "\" data-go-route-id=\"" + id + "\"></div>\n"
	if assets != nil {
		head += "<script type=\"module\" src=\"" + html.EscapeString(assets.EntryScriptURL) + "\"></script>\n"
	}
	head += "</body>\n" +
		"</html>\n"
	return head
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

// RouteAttr and RouteIDAttr are the stable data attributes the client
// runtime reads to identify the requested page.
const (
	RouteAttr   = "data-go-route"
	RouteIDAttr = "data-go-route-id"
)
