package httpserver

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"go-meta/internal/routing"
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
// invoking the page handler. Values are decoded once, after route segmentation.
func WithRouteParams(route routing.Route, next fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		params := make(map[string]string)
		for _, segment := range strings.Split(route.Path, "/") {
			if !strings.HasPrefix(segment, ":") {
				continue
			}
			name := segment[1:]
			value, err := url.PathUnescape(c.Params(name))
			if err != nil || value == "" || strings.ContainsAny(value, "/\\\x00") || value == "." || value == ".." || !utf8.ValidString(value) {
				return fiber.NewError(fiber.StatusBadRequest, "invalid URL parameter")
			}
			params[name] = strings.Clone(value)
		}
		c.Locals(routeParamsKey{}, params)
		return next(c)
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
