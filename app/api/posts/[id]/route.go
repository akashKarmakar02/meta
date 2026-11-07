//go:build ignore

// Package post demonstrates a dynamic file-based API endpoint.
package post

import "github.com/gofiber/fiber/v3"

// GET returns the ID from the URL.
func GET(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"id": c.Params("id")})
}

// DELETE demonstrates handling a method on a dynamic route.
func DELETE(c fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}
