package httpserver

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"go-meta/imageopt"
)

// MountImage reserves the image endpoint. F01 validates requests only; it
// never resolves sources or processes images. Register before page routes.
func MountImage(app *fiber.App) {
	mountImage(app, imageopt.DefaultLimits())
}

// MountImageWithLimits validates a server-specific, bounded request policy.
func MountImageWithLimits(app *fiber.App, limits imageopt.Limits) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	mountImage(app, limits)
	return nil
}

func mountImage(app *fiber.App, limits imageopt.Limits) {
	app.All(imageopt.Endpoint, func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
		if c.Method() != http.MethodGet && c.Method() != http.MethodHead {
			c.Set(fiber.HeaderAllow, "GET, HEAD")
			return c.Status(http.StatusMethodNotAllowed).SendString("method not allowed")
		}
		r, err := imageopt.ParseRawQuery(string(c.Request().URI().QueryString()))
		if err != nil || r.ValidateWithin(limits) != nil {
			return c.Status(http.StatusBadRequest).SendString("invalid image request")
		}
		return c.Status(http.StatusNotImplemented).SendString("image optimization is not implemented")
	})
}
