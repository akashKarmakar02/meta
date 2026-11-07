package server

import (
	"sort"
	"strings"

	internalrouting "github.com/akashKarmakar02/meta/internal/routing"
	"github.com/gofiber/fiber/v3"
)

// APIRoute is a generated file-based endpoint. Handlers must be configured
// by the application with authentication and request protections as needed.
type APIRoute struct {
	Path       string
	SourceFile string
	CatchAll   string
	Handlers   map[string]fiber.Handler
}

// MountAPI registers one endpoint. Callers must register routes in precedence order.
func MountAPI(app *fiber.App, route APIRoute, middleware ...fiber.Handler) {
	handlers := make(map[string]fiber.Handler, len(route.Handlers))
	methods := make([]string, 0, len(route.Handlers))
	for method, handler := range route.Handlers {
		handlers[method] = handler
		methods = append(methods, method)
	}
	sort.Strings(methods)
	allow := strings.Join(methods, ", ")
	handler := func(c fiber.Ctx) error {
		if handler, ok := handlers[c.Method()]; ok {
			return handler(c)
		}
		c.Set("Allow", allow)
		return c.SendStatus(fiber.StatusMethodNotAllowed)
	}
	if len(middleware) == 0 {
		app.All(internalrouting.FiberRegistrationPath(route.Path), handler)
		return
	}
	chain := make([]any, 0, len(middleware))
	for _, item := range middleware[1:] {
		chain = append(chain, item)
	}
	chain = append(chain, handler)
	app.All(internalrouting.FiberRegistrationPath(route.Path), middleware[0], chain...)
}
