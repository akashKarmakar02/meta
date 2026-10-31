// Package main is the example Fiber v3 executable for the go-meta framework.
package main

import (
	"log"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

// newApp constructs the Fiber application with its middleware and routes.
func newApp() *fiber.App {
	app := fiber.New()

	// Recover converts panics into 500 responses instead of killing the server.
	app.Use(recover.New())
	// Request logging; default config logs method, path, status, latency —
	// never request or response bodies.
	app.Use(logger.New())

	app.Get("/healthz", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	return app
}

func main() {
	log.Fatal(newApp().Listen(":3000"))
}
