// Package main is the example Fiber v3 executable for the go-meta framework.
package main

import (
	"log"

	"github.com/gofiber/fiber/v3"
)

// newApp constructs the Fiber application with its routes.
func newApp() *fiber.App {
	app := fiber.New()

	app.Get("/healthz", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	return app
}

func main() {
	log.Fatal(newApp().Listen(":3000"))
}
