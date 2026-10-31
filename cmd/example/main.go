// Package main is the example Fiber v3 executable for the go-meta framework.
package main

import (
	"log"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"

	routes "go-meta/generated"
	"go-meta/internal/render"
	"go-meta/internal/routing"
)

// newApp constructs the Fiber application with its middleware and routes.
func newApp() *fiber.App {
	fapp := fiber.New()

	// Recover converts panics into 500 responses instead of killing the server.
	fapp.Use(recover.New())
	// Request logging; default config logs method, path, status, latency —
	// never request or response bodies.
	fapp.Use(logger.New())

	fapp.Get("/healthz", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	// Generated page routes: registered from build-time codegen as plain
	// compiled data — no request-time discovery, no reflection. Order is
	// the manifest (path-sorted) order; paths are unique post-validation
	// (B08), so no route can be registered twice.
	for _, r := range routes.RegisterRoutes() {
		route := r
		switch route.Mode {
		case routing.ModeSSR:
			fapp.Get(route.Path, func(c fiber.Ctx) error {
				return routes.SSRPage(route).Render(c.RequestCtx(), c)
			})
		case routing.ModeCSR:
			fapp.Get(route.Path, render.ShellHandler(route))
		}
	}

	return fapp
}

func main() {
	log.Fatal(newApp().Listen(":3000"))
}
