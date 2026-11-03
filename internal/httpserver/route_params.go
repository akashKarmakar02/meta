package httpserver

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"go-meta/internal/routing"
	"go-meta/routeparams"
)

type routeParamsKey struct{}

// ValidatePagePath rejects encoded separators and traversal before Fiber's
// normalization can turn them into a different route. Trailing and repeated
// slashes retain Fiber's existing routing behavior.
func ValidatePagePath(c fiber.Ctx) error {
	if !safePagePath(string(c.Request().URI().PathOriginal())) {
		return fiber.NewError(fiber.StatusBadRequest, "invalid URL path")
	}
	return c.Next()
}

func safePagePath(path string) bool {
	for _, raw := range strings.Split(path, "/") {
		decoded, err := url.PathUnescape(raw)
		if err != nil || !utf8.ValidString(decoded) || strings.ContainsAny(decoded, "/\\\x00") || decoded == "." || decoded == ".." {
			return false
		}
	}
	return true
}

// WithRouteParams captures Fiber's matched parameters into a fresh map before
// invoking the page handler. Values are decoded once, after route
// segmentation. Named parameters are single segments; a catch-all ("+") is a
// slash-joined run of one or more segments, decoded and validated segment by
// segment and stored under the route's declared catch-all name (B03).
func WithRouteParams(route routing.Route, next fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		params := make(map[string]string)
		for _, segment := range strings.Split(route.Path, "/") {
			switch {
			case segment == "+":
				if route.CatchAll == "" {
					// A "+" segment without a declared name is a generator
					// bug, not a request condition; fail closed.
					return fiber.NewError(fiber.StatusInternalServerError, "catch-all route has no declared parameter name")
				}
				value, err := decodeCatchAll(c.Params("+"))
				if err != nil {
					return fiber.NewError(fiber.StatusBadRequest, "invalid URL parameter")
				}
				params[route.CatchAll] = value
			case strings.HasPrefix(segment, ":"):
				name := segment[1:]
				value, err := url.PathUnescape(c.Params(name))
				if err != nil || value == "" || strings.ContainsAny(value, "/\\\x00") || value == "." || value == ".." || !utf8.ValidString(value) {
					return fiber.NewError(fiber.StatusBadRequest, "invalid URL parameter")
				}
				params[name] = strings.Clone(value)
			}
		}
		c.Locals(routeParamsKey{}, params)
		bridgeParamsToRequestContext(c, params)
		return next(c)
	}
}

// decodeCatchAll splits Fiber's raw catch-all capture on real "/" separators
// and decodes each segment, rejecting the same unsafe values a single
// parameter rejects (encoded separators, traversal, invalid UTF-8). The result
// is the slash-joined decoded value, e.g. "getting-started/installation".
func decodeCatchAll(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty catch-all")
	}
	segments := strings.Split(raw, "/")
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		decoded, err := url.PathUnescape(seg)
		if err != nil || decoded == "" || strings.ContainsAny(decoded, "/\\\x00") || decoded == "." || decoded == ".." || !utf8.ValidString(decoded) {
			return "", errors.New("unsafe catch-all segment")
		}
		out = append(out, decoded)
	}
	return strings.Join(out, "/"), nil
}

// bridgeParamsToRequestContext publishes the request's decoded parameters on
// the underlying fasthttp RequestCtx so server-rendered templ pages can read
// them from the render context. It attaches two forms:
//
//   - a request-safe routeparams.Params (via routeparams.Attach), the
//     recommended typed accessor (routeparams.FromContext); and
//   - the B03 legacy string bridge (ctx.SetUserValue(name, value)), retained so
//     pages written before the typed API keep working.
//
// Both are request-scoped: fasthttp clears the RequestCtx user values when the
// pooled request is reset, so values never leak between requests. The broader
// query-aware route context is deferred to B05.
func bridgeParamsToRequestContext(c fiber.Ctx, params map[string]string) {
	ctx := c.RequestCtx()
	if ctx == nil {
		return
	}
	routeparams.Attach(ctx, routeparams.NewParams(params))
	for key, value := range params {
		ctx.SetUserValue(key, value)
	}
}

// RouteParams returns an independent snapshot of the request's matched
// parameters. It is internal to the framework, not the future public API.
func RouteParams(c fiber.Ctx) map[string]string {
	result := make(map[string]string)
	if params, ok := c.Locals(routeParamsKey{}).(map[string]string); ok {
		for key, value := range params {
			result[key] = value
		}
	}
	return result
}
