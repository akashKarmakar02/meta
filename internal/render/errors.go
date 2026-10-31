package render

import (
	"errors"

	"github.com/gofiber/fiber/v3"
)

// HTTPErrorHandler is the centralized D07 error handler: 404 and 405 get
// dedicated HTML pages, 5xx gets a generic page that never leaks internals
// or stack traces. Every response carries a unique, correct status code.
func HTTPErrorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	var ferr *fiber.Error
	if errors.As(err, &ferr) {
		code = ferr.Code
	}
	switch code {
	case fiber.StatusNotFound:
		return HTTPNotFoundHandler(c)
	case fiber.StatusMethodNotAllowed:
		return HTTPMethodNotAllowedHandler(c)
	default:
		if code >= 500 {
			c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
			return c.Status(code).SendString(
				"<!DOCTYPE html>\n" +
					"<html lang=\"en\">\n" +
					"<head><meta charset=\"utf-8\"><title>500 Server Error</title></head>\n" +
					"<body>\n" +
					"<main data-go-error=\"500\">\n" +
					"<h1>500 — Internal Server Error</h1>\n" +
					"<p>Something went wrong while handling your request.</p>\n" +
					"<p><a href=\"/\">Go home</a></p>\n" +
					"</main>\n" +
					"</body>\n" +
					"</html>\n")
		}
		// Non-HTTP errors keep Fiber's default JSON rendering for now; the
		// centralized HTML contract covers 404/405/5xx.
		return fiber.DefaultErrorHandler(c, err)
	}
}

// HTTPNotFoundHandler renders the centralized 404 page.
func HTTPNotFoundHandler(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Status(fiber.StatusNotFound).SendString("<!DOCTYPE html>\n" +
		"<html lang=\"en\">\n" +
		"<head><meta charset=\"utf-8\"><title>404 Not Found</title></head>\n" +
		"<body>\n" +
		"<main data-go-error=\"404\">\n" +
		"<h1>404 — Not Found</h1>\n" +
		"<p>The page you requested does not exist.</p>\n" +
		"<p><a href=\"/\">Go home</a></p>\n" +
		"</main>\n" +
		"</body>\n" +
		"</html>\n")
}

// HTTPMethodNotAllowedHandler renders the centralized 405 page.
func HTTPMethodNotAllowedHandler(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Status(fiber.StatusMethodNotAllowed).SendString(
		"<!DOCTYPE html>\n" +
			"<html lang=\"en\">\n" +
			"<head><meta charset=\"utf-8\"><title>405 Method Not Allowed</title></head>\n" +
			"<body>\n" +
			"<main data-go-error=\"405\">\n" +
			"<h1>405 — Method Not Allowed</h1>\n" +
			"<p>Method " + c.Method() + " is not allowed on this resource.</p>\n" +
			"</main>\n" +
			"</body>\n" +
			"</html>\n")
}
