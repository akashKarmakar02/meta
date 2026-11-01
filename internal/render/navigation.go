package render

import (
	"fmt"
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
// JSON envelope instead of a full document (blueprint 5.1).
func IsNavigationRequest(c fiber.Ctx) bool {
	return c.Get(httpserver.NavigationHeader) != ""
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
