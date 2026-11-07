// Package posts demonstrates a file-based API endpoint.
package posts

import "github.com/gofiber/fiber/v3"

// GET returns example posts.
func GET(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"posts": []fiber.Map{{"id": 42, "title": "Example post"}}})
}

// POST demonstrates reading a JSON body.
func POST(c fiber.Ctx) error {
	var input struct {
		Title string `json:"title"`
	}
	if err := c.Bind().JSON(&input); err != nil || input.Title == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title is required"})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"title": input.Title})
}
