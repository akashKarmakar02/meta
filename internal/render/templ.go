package render

import (
	"bytes"
	"context"
	"fmt"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v3"
)

// RenderComponent renders a templ component to bytes through an in-memory
// buffer. The ctx flows into templ's rendering pipeline so cancellation and
// deadline propagation behave as the caller expects; SSR handlers pass the
// request context (D05 wires that handoff).
func RenderComponent(ctx context.Context, component templ.Component) ([]byte, error) {
	var b bytes.Buffer
	if err := component.Render(ctx, &b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// SSRHandler returns a Fiber handler that renders a templ component for a
// fixed route and writes it as text/html — the D05 adapter keeping HTTP
// response details out of page handlers and components.
func SSRHandler(routePath string, component templ.Component) fiber.Handler {
	return func(c fiber.Ctx) error {
		html, err := RenderComponent(c.RequestCtx(), component)
		if err != nil {
			return fmt.Errorf("render %s: %w", routePath, err)
		}
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Send(html)
	}
}
