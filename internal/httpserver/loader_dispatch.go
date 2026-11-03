package httpserver

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gofiber/fiber/v3"

	"go-meta/internal/routing"
	"go-meta/loader"
	"go-meta/loaderdata"
	"go-meta/routecontext"
)

// WithLoader executes a route's registered loader exactly once, before the
// page renders, and attaches the result to the request. It is the C03/C04
// integration point: it sits between WithRouteParams (which builds the
// request-lifetime context and route snapshot) and the render handler.
//
// The loader is looked up by the route's stable ID in the immutable registry
// and, when present, invoked with the request context and the same snapshot
// the page will read. A nil registry or a route with no binding renders
// normally with no loader data attached.
//
// SSR routes (C03) receive the typed Go value through loaderdata for templ
// rendering. CSR routes (C04) receive the JSON-encoded value through
// WithCSRLoaderData, which the CSR handler embeds in the boot shell or
// attaches to the navigation envelope.
//
// On a loader error — or a JSON serialization error for a CSR result — the
// request stops before rendering: the original error is logged server-side and
// a generic 500 is returned. C03/C04 intentionally do not map loader sentinels
// to specific statuses (404/401/403/400/redirect) — that is C05. Cancellation
// errors are not logged as failures: the request was aborted, not mishandled.
func WithLoader(registry *loader.Registry, route routing.Route, next fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		if registry == nil || (route.Mode != routing.ModeSSR && route.Mode != routing.ModeCSR) {
			return next(c)
		}
		registration, ok := registry.Lookup(route.ID)
		if !ok {
			return next(c)
		}
		snapshot, ok := routecontext.FromContext(c.Context())
		if !ok {
			// WithRouteParams always attaches the snapshot for a matched route;
			// reaching here means the dispatch wiring is broken, so fail closed
			// rather than render a page with no data.
			return fiber.NewError(http.StatusInternalServerError, "loader route context missing")
		}
		result, err := registration.Execute(c.Context(), snapshot)
		if err != nil {
			if c.Context().Err() == nil {
				// The request was not cancelled; the loader itself failed.
				log.Printf("go-meta: loader for route %s failed: %v", route.ID, err)
			}
			return fiber.NewError(http.StatusInternalServerError, "loader failed")
		}
		if route.Mode == routing.ModeCSR {
			// C04: serialize the result to JSON for browser delivery. A
			// serialization failure (e.g. an unsupported type) stops the request
			// rather than publishing a page with missing data.
			data, err := json.Marshal(result)
			if err != nil {
				log.Printf("go-meta: loader result for route %s is not JSON-serializable: %v", route.ID, err)
				return fiber.NewError(http.StatusInternalServerError, "loader result not serializable")
			}
			c.SetContext(WithCSRLoaderData(c.Context(), data))
			return next(c)
		}
		c.SetContext(loaderdata.WithContext(c.Context(), result))
		return next(c)
	}
}
