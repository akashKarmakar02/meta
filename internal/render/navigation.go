package render

import (
	"fmt"
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

// NavigationEnvelope builds the navigation response for an SSR route:
// bare page outlet HTML (layouts persist client-side, blueprint 5.2
// step 9), route metadata, and layout chain by stable ID.
func NavigationEnvelope(route routing.Route, bareHTML string) httpserver.NavigationResponse {
	return httpserver.NavigationResponse{
		Version: httpserver.NavigationVersion,
		URL:     route.Path,
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
// clientEntry set to the stable source-derived route ID the generated
// import registry is keyed by (blueprint 5.1). Layouts report the
// discovered ancestry as it exists today, never fabricated chains.
func CSRNavigationEnvelope(route routing.Route) httpserver.NavigationResponse {
	entry := route.ID
	return httpserver.NavigationResponse{
		Version:     httpserver.NavigationVersion,
		URL:         route.Path,
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

// SSRNavigationHandler adapts an SSR route into a Fiber handler serving
// either the composed full document (normal GET) or the bare page outlet
// inside a navigation envelope (header present). The resolvers come from
// generated code; render stays decoupled from the routes package.
func SSRNavigationHandler(route routing.Route, composed, bare PageResolver) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !IsNavigationRequest(c) {
			component, err := composed(route)
			if err != nil {
				return fmt.Errorf("routes: SSRPage: %w", err)
			}
			return SSRHandler(route.Path, component)(c)
		}
		bareComponent, err := bare(route)
		if err != nil {
			return fmt.Errorf("routes: BarePage: %w", err)
		}
		bareHTML, err := RenderComponent(c.RequestCtx(), bareComponent)
		if err != nil {
			return fmt.Errorf("render bare page %s: %w", route.Path, err)
		}
		return SendNavigation(c, NavigationEnvelope(route, string(bareHTML)))
	}
}

// CSRNavigationHandler adapts a CSR route into a Fiber handler serving
// either the boot shell (normal GET, E10 contract) or a navigation
// envelope describing what the browser must mount (header present).
// The server never renders Svelte components; html stays empty and
// clientEntry carries the registry key for the browser to resolve
// through the generated static import map.
func CSRNavigationHandler(route routing.Route, assets *httpserver.Assets) fiber.Handler {
	shell := ShellHandler(route, assets)
	return func(c fiber.Ctx) error {
		if !IsNavigationRequest(c) {
			return shell(c)
		}
		return SendNavigation(c, CSRNavigationEnvelope(route))
	}
}
