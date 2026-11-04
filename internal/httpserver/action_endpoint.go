// D02/D03: reserved, method-restricted JSON and form HTTP endpoints for
// registered actions.
package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"go-meta/action"
	"go-meta/formcsrf"
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
	// CSRF is the form-CSRF token manager. It is required for
	// application/x-www-form-urlencoded submissions (double-submit cookie
	// verification); JSON submissions never consult it. Nil keeps the JSON
	// path fully functional and fails form requests closed.
	CSRF *formcsrf.Manager
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
	c.Set(fiber.HeaderCacheControl, "no-store")
	id := strings.TrimPrefix(c.Path(), ActionsPrefix)
	if !action.ValidID(id) {
		// Malformed IDs answer exactly like unknown-but-valid IDs: a safe 404
		// with no information about the registry.
		return actionError(c, fiber.StatusNotFound, "unknown action")
	}
	if !sameOrigin(c, cfg.TrustedOrigin) {
		if contentTypeOf(c.Get(fiber.HeaderContentType)) == "application/json" {
			return actionError(c, http.StatusForbidden, "cross-origin action request rejected")
		}
		return formErrorPage(c, http.StatusForbidden, "Requests from other sites are not accepted.")
	}
	switch contentTypeOf(c.Get(fiber.HeaderContentType)) {
	case "application/json":
		return dispatchJSONAction(c, registry, cfg, id)
	case "application/x-www-form-urlencoded":
		return dispatchFormAction(c, registry, cfg, id)
	default:
		// Content-Type gate runs before reading the body: c.Body() would
		// otherwise try to parse a multipart payload just to measure it.
		return actionError(c, http.StatusUnsupportedMediaType, "unsupported content type for actions")
	}
}

// contentTypeOf extracts the media type from a Content-Type header value,
// lowercased, ignoring any parameters such as charset. An empty header
// yields "".
func contentTypeOf(v string) string {
	if v == "" {
		return ""
	}
	mediaType := v
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

// dispatchFormAction is the D03 browser-native path. Execution order:
// body-size gate → parse form values → CSRF verify (field must match the
// signed cookie, both verified by the manager) → registry lookup →
// form-enabled gate → typed decode → Execute once → safe 303 PRG redirect.
// The action never runs before every gate passes; the JSON path stays
// byte-for-byte compatible with D02.
func dispatchFormAction(c fiber.Ctx, registry *action.Registry, cfg ActionEndpointConfig, id string) error {
	enhanced := enhancedForm(c)
	if int64(len(c.Body())) > cfg.MaxBodyBytes {
		return formErrorPage(c, http.StatusRequestEntityTooLarge, "The submission was too large.")
	}
	values, err := parseURLEncodedForm(string(c.Body()))
	if err != nil {
		return formErrorPage(c, http.StatusBadRequest, "The submitted form could not be read.")
	}
	for name := range values {
		if len(name) > 256 || !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return formErrorPage(c, http.StatusBadRequest, "The submitted form could not be read.")
		}
	}
	// CSRF verification happens before registry lookup and decoding: without a
	// valid token no information about the binding or input shapes is revealed
	// and nothing executes. Double-submit: the hidden field must equal the
	// signed _meta_csrf cookie and both must carry a valid signature.
	binding, ok := registry.Lookup(id)
	if !ok {
		// Unknown-but-valid IDs answer identically to malformed ones: a safe
		// 404 with no information about the registry.
		return formErrorPage(c, fiber.StatusNotFound, "Unknown action.")
	}
	if !binding.FormCapable() {
		// The action exists but has opted out of form submissions. Answering
		// with 415 (not a look-alike 404) tells a browser form author their
		// form targets the wrong transport, without leaking anything about
		// other bindings.
		return formErrorPage(c, http.StatusUnsupportedMediaType, "This form is not available.")
	}
	if cfg.CSRF == nil {
		log.Printf("go-meta: form action %s rejected: no CSRF manager configured", id)
		return formErrorPage(c, http.StatusInternalServerError, "This form is not available right now.")
	}
	formToken := values.Get(formcsrf.FieldName)
	if formToken == "" {
		return formErrorPage(c, http.StatusForbidden, "Missing CSRF token. Reload the page and try again.")
	}
	cookieName := formcsrf.CookieName
	if strings.HasPrefix(strings.ToLower(cfg.TrustedOrigin), "https://") {
		cookieName = formcsrf.SecureCookieName
	}
	cookieToken := c.Cookies(cookieName)
	if !cfg.CSRF.Verify(formToken, cookieToken) {
		return formErrorPage(c, http.StatusForbidden, "Invalid CSRF token. Reload the page and try again.")
	}
	delete(values, formcsrf.FieldName)
	input, derr := binding.DecodeForm(values)
	if derr != nil {
		var validation *action.ValidationError
		if errors.As(derr, &validation) && validation != nil {
			if enhanced {
				return handleActionError(c, id, derr, false)
			}
			return formValidation(c, binding, values, validation, cookieToken)
		}
		return handleActionError(c, id, fmt.Errorf("%w: %v", action.ErrInvalidInput, derr), !enhanced)
	}
	route := actionRouteSnapshot(c, id)
	if _, err = binding.InvokeForm(c.Context(), route, input); err != nil {
		var validation *action.ValidationError
		if errors.As(err, &validation) && validation != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			if enhanced {
				return handleActionError(c, id, err, false)
			}
			return formValidation(c, binding, values, validation, cookieToken)
		}
		return handleActionError(c, id, err, !enhanced)
	}
	// Post/Redirect/Get with the binding's trusted destination. Result values
	// are not placed in the redirect URL (D04 may extend this).
	dest := binding.SuccessURL()
	if loc, ok := validFormRedirectLocation(dest); ok {
		c.Set(fiber.HeaderCacheControl, "no-store")
		if enhanced {
			data, err := json.Marshal(struct {
				OK       bool   `json:"ok"`
				Redirect string `json:"redirect"`
			}{OK: true, Redirect: loc})
			if err != nil {
				return actionError(c, http.StatusInternalServerError, "action failed")
			}
			c.Set(fiber.HeaderContentType, "application/json")
			return c.Status(http.StatusOK).Send(data)
		}
		return c.Redirect().Status(fiber.StatusSeeOther).To(loc)
	}
	log.Printf("go-meta: action %s has invalid redirect target %q", id, dest)
	return formErrorPage(c, http.StatusInternalServerError, "Something went wrong. Please try again.")
}

// dispatchJSONAction is the D02 path, unchanged in behavior: body-size gate →
// registry lookup → strict JSON decode → Execute once → JSON response.
func dispatchJSONAction(c fiber.Ctx, registry *action.Registry, cfg ActionEndpointConfig, id string) error {
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
		return handleActionError(c, id, err, false)
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

// handleActionError maps action errors to the HTTP contract using errors.Is,
// logging only genuine failures server-side. It never leaks the underlying
// error to the client. The mapping parallels C05's loader mapping for the
// shared vocabulary while remaining separate: action errors never reuse loader
// types or endpoints. htmlMode selects the browser-form response shape; JSON
// clients keep the D02 JSON contract.
func handleActionError(c fiber.Ctx, id string, err error, htmlMode bool) error {
	switch {
	case errors.Is(err, context.Canceled):
		// The client aborted mid-action; not logged (C05 policy). The action
		// may have committed its effect — cancellation is not a rollback.
		if htmlMode {
			return formErrorPage(c, statusClientClosedRequest, "The request was canceled.")
		}
		return actionError(c, statusClientClosedRequest, "client canceled request")
	case errors.Is(err, context.DeadlineExceeded):
		log.Printf("go-meta: action %s exceeded its deadline: %v", id, err)
		if htmlMode {
			return formErrorPage(c, http.StatusGatewayTimeout, "The request timed out.")
		}
		return actionError(c, http.StatusGatewayTimeout, "action timed out")
	case isValidation(err):
		var validation *action.ValidationError
		errors.As(err, &validation)
		data, marshalErr := json.Marshal(struct {
			OK      bool                `json:"ok"`
			Error   string              `json:"error"`
			Message string              `json:"message,omitempty"`
			Errors  map[string][]string `json:"errors"`
		}{Error: "validation failed", Message: validation.Message(), Errors: validation.Fields()})
		if marshalErr != nil {
			return actionError(c, http.StatusInternalServerError, "action failed")
		}
		c.Set(fiber.HeaderContentType, "application/json")
		c.Set(fiber.HeaderCacheControl, "no-store")
		return c.Status(http.StatusUnprocessableEntity).Send(data)
	case errors.Is(err, action.ErrInvalidInput):
		// Includes strict JSON/form decode failures from the adapter.
		// Expected client error: not logged, never retried. D04 will add
		// field-level feedback for forms.
		if htmlMode {
			return formErrorPage(c, http.StatusBadRequest, "Some form fields could not be read correctly.")
		}
		return actionError(c, http.StatusBadRequest, "invalid input")
	case errors.Is(err, action.ErrUnauthorized):
		if htmlMode {
			return formErrorPage(c, http.StatusUnauthorized, "You need to sign in to do that.")
		}
		return actionError(c, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, action.ErrForbidden):
		if htmlMode {
			return formErrorPage(c, http.StatusForbidden, "You are not allowed to do that.")
		}
		return actionError(c, http.StatusForbidden, "forbidden")
	case errors.Is(err, action.ErrNotFound):
		if htmlMode {
			return formErrorPage(c, http.StatusNotFound, "Not found.")
		}
		return actionError(c, http.StatusNotFound, "not found")
	default:
		log.Printf("go-meta: action %s failed: %v", id, err)
		if htmlMode {
			return formErrorPage(c, http.StatusInternalServerError, "Something went wrong. Please try again.")
		}
		return actionError(c, http.StatusInternalServerError, "action failed")
	}
}

func isValidation(err error) bool {
	var validation *action.ValidationError
	return errors.As(err, &validation) && validation != nil
}

func formValidation(c fiber.Ctx, binding action.Binding, values url.Values, validation *action.ValidationError, token string) error {
	component := binding.FormFeedback(values, validation)
	if component == nil {
		return formErrorPage(c, http.StatusUnprocessableEntity, "Please correct the submitted form and try again.")
	}
	var buf bytes.Buffer
	ctx := formcsrf.WithContext(c.Context(), token)
	if err := component.Render(ctx, &buf); err != nil {
		log.Printf("go-meta: form feedback render failed: %v", err)
		return formErrorPage(c, http.StatusInternalServerError, "Something went wrong. Please try again.")
	}
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(http.StatusUnprocessableEntity).Send(buf.Bytes())
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

// csrfFormField is the fixed name of the hidden CSRF input rendered by
// formcsrf.HiddenInput. The framework reserves this field name.
const csrfFormField = "_csrf"

// formErrorPage answers a browser-native form failure with a minimal, safe
// HTML page: static text only, no reflection of user input, no internal
// details. D04 will add field-level feedback and value preservation.
func enhancedForm(c fiber.Ctx) bool {
	return contentTypeOf(c.Get(fiber.HeaderContentType)) == "application/x-www-form-urlencoded" && c.Get("X-Go-Meta-Action") == "1"
}

func formErrorPage(c fiber.Ctx, status int, msg string) error {
	if enhancedForm(c) {
		return actionError(c, status, msg)
	}
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	c.Set(fiber.HeaderCacheControl, "no-store")
	body := "<!doctype html><html><head><meta charset=\"utf-8\"><title>Submission failed</title></head>" +
		"<body><h1>Submission failed</h1><p>" + html.EscapeString(msg) + "</p>" +
		"<p><a href=\"/\">Go to the home page</a></p></body></html>"
	return c.Status(status).SendString(body)
}

// parseURLEncodedForm parses an application/x-www-form-urlencoded body with
// standard url.Values semantics: repeated keys accumulate in order, percent
// escapes decode, '+' decodes as space, missing '=' yields an empty value.
// Malformed percent encodings are rejected rather than silently corrected.
func parseURLEncodedForm(body string) (url.Values, error) {
	if body == "" {
		return url.Values{}, nil
	}
	return url.ParseQuery(body)
}

// validFormRedirectLocation applies the C05 safe-redirect policy to a form
// success destination: same-origin absolute-path targets only, no scheme,
// host, protocol-relative, back-targeting, or double-slash forms.
func validFormRedirectLocation(dest string) (string, bool) {
	if validRedirectLocation(dest) {
		return dest, true
	}
	return "", false
}
