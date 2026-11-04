// D02: reserved, method-restricted JSON HTTP endpoints for registered actions.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"

	"go-meta/action"
	"go-meta/routecontext"
	"go-meta/routeparams"
)

// ActionsPrefix is the framework-owned endpoint namespace for action dispatch.
// It is registered before page routes and cannot be shadowed by an application
// page route: generated page paths come from app/ source files and never start
// with "/_meta".
const ActionsPrefix = "/_meta/actions/"

// DefaultMaxActionBodyBytes is the default request-body limit for action
// endpoints: 1 MiB, enforced against the body actually received (fasthttp
// buffers request bodies under the app-level BodyLimit; this check bounds what
// an action dispatch accepts regardless of Content-Length claims).
const DefaultMaxActionBodyBytes = 1 << 20

// ActionEndpointConfig configures the action endpoint. TrustedOrigin is the
// single origin action requests must originate from ("scheme://host[:port]",
// e.g. "http://localhost:3000" in development, "https://example.com" in
// production). MaxBodyBytes bounds the JSON request body (default 1 MiB).
type ActionEndpointConfig struct {
	TrustedOrigin string
	MaxBodyBytes  int64
}

// MountActions registers POST /_meta/actions/{actionID} on app. A nil registry
// still reserves the namespace so no page route can claim it; every ID then
// answers 404.
//
// Method policy is enforced by Fiber's method-specific registration: non-POST
// methods on the path fall through to the app's 405 handling and can never
// reach the action handler. Page navigation, prefetching, SSR, CSR, and loader
// dispatch are all GET-based, so none can invoke an action.
//
// Execution order per accepted POST: action ID syntax → Origin check →
// body-size check → Content-Type check → registry lookup → strict JSON decode
// → action Execute (once) → JSON response. The action runs only after every
// security and decoding gate passes.
func MountActions(app *fiber.App, registry *action.Registry, cfg ActionEndpointConfig) {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = DefaultMaxActionBodyBytes
	}
	app.Post(ActionsPrefix+"*", func(c fiber.Ctx) error {
		return dispatchAction(c, registry, cfg)
	})
}

func dispatchAction(c fiber.Ctx, registry *action.Registry, cfg ActionEndpointConfig) error {
	id := strings.TrimPrefix(c.Path(), ActionsPrefix)
	if !action.ValidID(id) {
		// Malformed IDs answer exactly like unknown-but-valid IDs: a safe 404
		// with no information about the registry.
		return actionError(c, fiber.StatusNotFound, "unknown action")
	}
	if !sameOrigin(c, cfg.TrustedOrigin) {
		return actionError(c, http.StatusForbidden, "cross-origin action request rejected")
	}
	// Content-Type gate runs before reading the body: c.Body() would otherwise
	// try to parse a multipart payload just to measure it.
	if !isJSONContentType(c.Get(fiber.HeaderContentType)) {
		return actionError(c, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
	}
	if int64(len(c.Body())) > cfg.MaxBodyBytes {
		return actionError(c, http.StatusRequestEntityTooLarge, "request body too large")
	}
	binding, ok := registry.Lookup(id)
	if !ok {
		return actionError(c, fiber.StatusNotFound, "unknown action")
	}

	route := actionRouteSnapshot(c, id)
	result, err := binding.Invoke(c.Context(), route, c.Body())
	if err != nil {
		return handleActionError(c, id, err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		log.Printf("go-meta: action %s result is not JSON-serializable: %v", id, err)
		return actionError(c, http.StatusInternalServerError, "action result not serializable")
	}
	c.Set(fiber.HeaderContentType, "application/json")
	// Mutation responses must never be cached.
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(fiber.StatusOK).SendString(`{"ok":true,"data":` + string(data) + `}`)
}

// actionRouteSnapshot builds the action's routecontext.Route. An action
// endpoint is not a page route: RouteID names the action ("action:"+id),
// Pattern is the endpoint pattern itself, Params is empty (a flat action ID
// has no path parameters), and Query carries the request query under the same
// bounded parsing policy as page dispatch.
func actionRouteSnapshot(c fiber.Ctx, id string) routecontext.Route {
	return routecontext.Route{
		RouteID: "action:" + id,
		Pattern: ActionsPrefix + id,
		Params:  emptyParams(),
		Query:   routecontext.NewQuery(parseActionQuery(c)),
	}
}

func emptyParams() routeparams.Params {
	return routeparams.NewParams(nil)
}

func parseActionQuery(c fiber.Ctx) url.Values {
	raw := string(c.Request().URI().QueryString())
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxQueryBytes {
		return nil // the page-path policy rejects oversized queries; so do we
	}
	query, err := url.ParseQuery(strings.Clone(raw))
	if err != nil {
		return nil
	}
	return query
}

// handleActionError maps action errors to the D02 HTTP contract using
// errors.Is, logging only genuine failures server-side. It never leaks the
// underlying error to the client. The mapping parallels C05's loader mapping
// for the shared vocabulary while remaining separate: action errors never
// reuse loader types or endpoints.
func handleActionError(c fiber.Ctx, id string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		// The client aborted mid-action; not logged (C05 policy). The action
		// may have committed its effect — cancellation is not a rollback.
		return actionError(c, statusClientClosedRequest, "client canceled request")
	case errors.Is(err, context.DeadlineExceeded):
		log.Printf("go-meta: action %s exceeded its deadline: %v", id, err)
		return actionError(c, http.StatusGatewayTimeout, "action timed out")
	case errors.Is(err, action.ErrInvalidInput):
		// Includes strict JSON decode failures from the adapter. Expected
		// client error: not logged, never retried.
		return actionError(c, http.StatusBadRequest, "invalid input")
	case errors.Is(err, action.ErrUnauthorized):
		return actionError(c, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, action.ErrForbidden):
		return actionError(c, http.StatusForbidden, "forbidden")
	case errors.Is(err, action.ErrNotFound):
		return actionError(c, http.StatusNotFound, "not found")
	default:
		log.Printf("go-meta: action %s failed: %v", id, err)
		return actionError(c, http.StatusInternalServerError, "action failed")
	}
}

// isJSONContentType accepts application/json with an optional charset
// parameter. Any other media type is rejected with 415 — in particular HTML
// form content types (urlencoded/multipart/text/plain) can never execute an
// action, which closes the "simple request" CSRF vector alongside the Origin
// check.
func isJSONContentType(ct string) bool {
	mt := strings.TrimSpace(strings.ToLower(ct))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = mt[:i]
	}
	return strings.TrimSpace(mt) == "application/json"
}

// sameOrigin enforces the D02 origin/CSRF boundary for the JSON endpoint:
//
//   - A browser POST carries Origin on same-origin and cross-site requests;
//     a mismatch is rejected.
//   - Missing Origin fails closed: this is a browser-facing mutation surface,
//     not an API entry point. (When D03 adds non-JS form support it must bring
//     its own CSRF token path; see README.)
//   - Malformed, non-http(s), and protocol-relative origins are invalid.
//   - TrustedOrigin is explicit configuration, never inferred from forwarded
//     headers: X-Forwarded-Host/Proto from an untrusted client cannot move the
//     goalposts because they are not consulted at all. The browser fixes Host
//     to the target; the dev proxy forwards Host unchanged.
//
// Together with the Content-Type gate this blocks every HTML-form CSRF vector:
// a form post cannot carry application/json, and fetch with that content type
// is not a simple request, so it requires CORS permission — which this
// endpoint never grants.
//
// Limitation (documented, deliberate): the framework ships no session cookies
// today, so there is no ambient credential for CSRF to abuse. When sessions
// arrive, a token mechanism is required on top of this check.
func sameOrigin(c fiber.Ctx, trusted string) bool {
	if trusted == "" {
		return false // unconfigured = fail closed
	}
	origin := c.Get("Origin")
	if !validOriginFormat(origin) {
		return false
	}
	return strings.EqualFold(origin, trusted)
}

// validOriginFormat reports whether an Origin value parses as a plain http(s)
// origin: scheme + host, no path/query/fragment/userinfo/opaque data.
func validOriginFormat(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil && u.Opaque == ""
}

// actionError writes the stable JSON error representation. The message is
// class-level only; the underlying error never reaches the client.
func actionError(c fiber.Ctx, status int, msg string) error {
	c.Set(fiber.HeaderContentType, "application/json")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(status).SendString(fmt.Sprintf(`{"ok":false,"error":%q}`, msg))
}
