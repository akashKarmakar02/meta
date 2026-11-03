package render

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v3"

	"go-meta/internal/httpserver"
	"go-meta/internal/routing"
)

// PageResolver returns a templ component for a route; generated code
// supplies the concrete mappings (routes.SSRPage / routes.BarePage).
type PageResolver func(routing.Route) (templ.Component, error)

// IsNavigationRequest reports whether a GET asks for the soft-navigation
// JSON envelope instead of a full document (blueprint 5.1). Only the
// supported protocol version counts: an unsupported version degrades to
// a normal full-page response rather than an envelope (blueprint 5.1).
func IsNavigationRequest(c fiber.Ctx) bool {
	return c.Get(httpserver.NavigationHeader) == strconv.Itoa(httpserver.NavigationVersion)
}

// layoutIDs returns the stable layout identifiers for a route,
// outermost to innermost, never nil so JSON serializes as [].
func layoutIDs(r routing.Route) []string {
	ids := make([]string, len(r.Layouts))
	for i, l := range r.Layouts {
		ids[i] = l.ID
	}
	return ids
}

// extractTitle pulls the first h1 text out of rendered HTML as the
// navigation title. The blueprint wire examples ("About", "Settings")
// are exactly the pages' headings. Empty when no heading exists; the
// client then leaves the document title unchanged.
func extractTitle(html string) string {
	const open = "<h1"
	i := strings.Index(html, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	gt := strings.IndexByte(html[i:], '>')
	if gt < 0 {
		return ""
	}
	i += gt + 1
	end := strings.Index(html[i:], "</h1>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(html[i : i+end])
}

// requestURL returns the URL the client actually requested: path plus
// query. The blueprint's url field means "the address being navigated
// to" — the client pushes it to history verbatim (blueprint 5.2 step
// 10), so dynamic-route patterns like /products/:id must not leak into
// envelopes; the concrete request path is used instead. The hash never
// reaches the server and is re-attached client-side.
func requestURL(c fiber.Ctx) string {
	u := string(c.Request().URI().PathOriginal())
	if q := string(c.Request().URI().QueryString()); q != "" {
		u += "?" + q
	}
	return u
}

// NavigationEnvelope builds an SSR envelope and extracts the page title.
// The handler replaces HTML with the selected visual layout and page.
func NavigationEnvelope(c fiber.Ctx, route routing.Route, bareHTML string) httpserver.NavigationResponse {
	return httpserver.NavigationResponse{
		Version: httpserver.NavigationVersion,
		URL:     requestURL(c),
		RouteID: route.ID,
		Mode:    string(route.Mode),
		Title:   extractTitle(bareHTML),
		Layouts: layoutIDs(route),
		HTML:    bareHTML,
	}
}

// csrTitle resolves the navigation title for a CSR route from its path:
// the final non-empty segment, first letter capitalized ("/dashboard" =>
// "Dashboard", "/dashboard/settings" => "Settings"). The server cannot
// render Svelte components, so unlike SSR there is no markup to scan;
// route identity carries the title instead. Empty for the bare root.
func csrTitle(path string) string {
	seg := strings.Trim(path, "/")
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	if seg == "" {
		return ""
	}
	return strings.ToUpper(seg[:1]) + seg[1:]
}

// CSRNavigationEnvelope builds the navigation response for a CSR route:
// no server-rendered HTML — the browser mounts the page component — with
// clientEntry set to the stable source-derived route ID. The handler adds
// server-rendered visual-layout HTML when the selected layout is templ.
func CSRNavigationEnvelope(c fiber.Ctx, route routing.Route) httpserver.NavigationResponse {
	entry := route.ID
	return httpserver.NavigationResponse{
		Version:     httpserver.NavigationVersion,
		URL:         requestURL(c),
		RouteID:     route.ID,
		Mode:        string(route.Mode),
		Title:       csrTitle(route.Path),
		Layouts:     layoutIDs(route),
		HTML:        "",
		ClientEntry: &entry,
	}
}

// SendNavigation writes an envelope as JSON with the no-store cache
// semantics the blueprint requires of navigation responses.
func SendNavigation(c fiber.Ctx, resp httpserver.NavigationResponse) error {
	data, err := httpserver.MarshalNavigation(resp)
	if err != nil {
		return fmt.Errorf("marshal navigation response: %w", err)
	}
	c.Set(fiber.HeaderContentType, "application/json")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Send(data)
}

// entryTags renders the client-entry asset tags (module script and any
// entry CSS links) injected into served documents. Nil assets means the
// fixture/test mode: no tags, no client runtime — matches the CSR shell.
func entryTags(assets *httpserver.Assets) string {
	if assets == nil {
		return ""
	}
	var b strings.Builder
	if assets.DevClientURL != "" {
		b.WriteString(`<script type="module" src="`)
		b.WriteString(html.EscapeString(assets.DevClientURL))
		b.WriteString(`"></script>`)
	}
	for _, css := range assets.EntryCSSURLs {
		b.WriteString(`<link rel="stylesheet" href="`)
		b.WriteString(html.EscapeString(css))
		b.WriteString(`">`)
	}
	b.WriteString(`<script type="module" src="`)
	b.WriteString(html.EscapeString(assets.EntryScriptURL))
	b.WriteString(`"></script>`)
	return b.String()
}

// InjectBeforeTag inserts tags immediately before the first closing tag
// matching tag (e.g. </body>). Documents without the tag come back
// unchanged — a malformed document is served as rendered, never mangled.
func InjectBeforeTag(document, tag, tags string) string {
	if tags == "" {
		return document
	}
	i := strings.Index(document, tag)
	if i < 0 {
		return document
	}
	return document[:i] + tags + document[i:]
}

// SSRNavigationHandler adapts an SSR route into a Fiber handler serving
// either the composed full document (normal GET, with the client entry
// script so soft navigation boots on server-rendered pages too — blueprint
// §6: the backend emits manifest-resolved asset URLs into HTML) or the
// selected visual layout and page in a navigation envelope. The
// resolvers come from generated code; render stays decoupled from the
// routes package. Nil assets keeps the fixture/test mode scriptless.
func SSRNavigationHandler(route routing.Route, assets *httpserver.Assets, composed, bare PageResolver, resolve VisualLayoutResolver) fiber.Handler {
	tags := entryTags(assets)
	return func(c fiber.Ctx) error {
		if !IsNavigationRequest(c) {
			component, err := composed(route)
			if err != nil {
				return fmt.Errorf("routes: SSRPage: %w", err)
			}
			htmlDoc, err := RenderComponent(c.Context(), component)
			if err != nil {
				return fmt.Errorf("render %s: %w", route.Path, err)
			}
			c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
			return c.SendString(document(string(htmlDoc), tags, "go-meta"))
		}
		bareComponent, err := bare(route)
		if err != nil {
			return fmt.Errorf("routes: BarePage: %w", err)
		}
		bareHTML, err := RenderComponent(c.Context(), bareComponent)
		if err != nil {
			return fmt.Errorf("render bare page %s: %w", route.Path, err)
		}
		// bareHTML is already escaped, server-owned templ output. Compose it
		// once so the title and visible page come from the same render.
		visual, err := resolve(route, templ.Raw(string(bareHTML)))
		if err != nil {
			return fmt.Errorf("routes: VisualLayout: %w", err)
		}
		visualHTML, err := RenderComponent(c.Context(), visual)
		if err != nil {
			return fmt.Errorf("render visual layout %s: %w", route.Path, err)
		}
		env := NavigationEnvelope(c, route, string(bareHTML))
		env.HTML = string(visualHTML)
		return SendNavigation(c, env)
	}
}

// CSRNavigationHandler adapts a CSR route into a Fiber handler serving
// either the boot shell (normal GET, E10 contract) or a navigation
// envelope describing what the browser must mount (header present).
// The server never renders Svelte components; HTML contains only a templ
// visual layout and mount point when such a layout is selected.
func CSRNavigationHandler(route routing.Route, assets *httpserver.Assets, resolvers ...VisualLayoutResolver) fiber.Handler {
	var resolve VisualLayoutResolver
	if len(resolvers) > 0 {
		resolve = resolvers[0]
	}
	return func(c fiber.Ctx) error {
		visual, err := csrVisual(c.Context(), route, resolve, string(c.Request().URI().PathOriginal()))
		if err != nil {
			return err
		}
		if !IsNavigationRequest(c) {
			c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
			return c.SendString(document(visual, entryTags(assets), route.ID))
		}
		env := CSRNavigationEnvelope(c, route)
		if len(route.Layouts) > 0 && route.Layouts[0].Mode == routing.ModeSSR {
			env.HTML = visual
		}
		return SendNavigation(c, env)
	}
}
