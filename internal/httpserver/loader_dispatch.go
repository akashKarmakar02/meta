package httpserver

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/gofiber/fiber/v3"

	"go-meta/internal/routing"
	"go-meta/loader"
	"go-meta/loaderdata"
	"go-meta/routecontext"
)

// WithLoader resolves a route's registered loader result once, before the
// page renders, and attaches the result to the request. It is the C03/C04/C05
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
// Loader outcomes (C05) are mapped to HTTP before the page renders:
//
//   - *loader.RedirectError → 302 with a validated root-relative Location
//     (open-redirect defense). Validated BEFORE any response is written; an
//     invalid target fails closed as a 500.
//   - loader.ErrNotFound / ErrUnauthorized / ErrForbidden / ErrInvalidInput →
//     404 / 401 / 403 / 400. Wrapped errors classify via errors.Is.
//   - context.Canceled → 499 (client closed the connection); NOT logged as a
//     failure — the request was aborted, not mishandled.
//   - context.DeadlineExceeded → 504; logged.
//   - anything else → 500; the real error is logged server-side but the client
//     sees only a generic message (no internals leak).
//
// A JSON serialization error for a CSR result is an internal 500: the data
// existed but could not be published, so the request stops rather than
// rendering a page with missing data.
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
			return handleLoaderError(c, route.ID, err)
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

// handleLoaderError maps a loader error to its HTTP outcome (C05). It is the
// single place where loader failures become responses, so the status mapping,
// logging policy, and redirect validation stay consistent across SSR and CSR,
// direct and soft-navigation requests.
func handleLoaderError(c fiber.Ctx, routeID string, err error) error {
	switch classifyLoaderError(err) {
	case outcomeRedirect:
		var redirect *loader.RedirectError
		_ = errors.As(err, &redirect)
		if !validRedirectLocation(redirect.Location) {
			// An invalid target is a loader bug, not a client condition: fail
			// closed with a 500 rather than emit a redirect the client could
			// follow off-origin. The real target is logged for diagnosis.
			log.Printf("go-meta: loader for route %s returned an invalid redirect location %q; refusing", routeID, redirect.Location)
			return fiber.NewError(http.StatusInternalServerError, "invalid loader redirect")
		}
		// A redirect is a successful outcome, not a failure: no error log.
		// The status + Location are set directly (no Fiber Redirect helper) so
		// the response is a bare 302 with no body — the browser (direct load)
		// and fetchNavigation (soft navigation, redirect: follow) both follow
		// it transparently.
		c.Set(fiber.HeaderLocation, redirect.Location)
		return c.Status(redirectStatus).SendString("")
	case outcomeCanceled:
		// The client aborted the request (closed the tab, navigated away).
		// 499 is the nginx convention for "client closed request". Deliberately
		// NOT logged: it is not a server failure, and logging every aborted
		// request would turn normal user behavior into noise.
		return fiber.NewError(statusClientClosedRequest, "client canceled request")
	case outcomeDeadline:
		log.Printf("go-meta: loader for route %s exceeded its deadline: %v", routeID, err)
		return fiber.NewError(http.StatusGatewayTimeout, "loader timed out")
	case outcomeNotFound:
		return fiber.NewError(http.StatusNotFound, "not found")
	case outcomeUnauthorized:
		return fiber.NewError(http.StatusUnauthorized, "unauthorized")
	case outcomeForbidden:
		return fiber.NewError(http.StatusForbidden, "forbidden")
	case outcomeInvalidInput:
		return fiber.NewError(http.StatusBadRequest, "invalid input")
	default:
		// Internal: the real error is logged server-side; the client sees only
		// a generic message so no internals (paths, stack fragments, DB errors)
		// leak into the response.
		log.Printf("go-meta: loader for route %s failed: %v", routeID, err)
		return fiber.NewError(http.StatusInternalServerError, "loader failed")
	}
}
