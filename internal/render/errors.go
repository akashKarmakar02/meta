package render

import (
	"errors"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

// HTTPErrorHandler is the centralized D07 error handler: 404 and 405 get
// dedicated HTML pages, every other 4xx and 5xx gets a status-specific HTML
// page that never leaks internals or stack traces. Every response carries a
// unique, correct status code.
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
		if code >= 400 && code < 600 {
			return renderHTTPStatusPage(c, code)
		}
		// Non-HTTP errors keep Fiber's default JSON rendering for now; the
		// centralized HTML contract covers 4xx/5xx.
		return fiber.DefaultErrorHandler(c, err)
	}
}

// statusPageCopy maps the statuses the framework can produce to their page
// title and body. The message is generic by design: it tells the user what
// happened at the HTTP level without revealing server internals (which loader
// failed, why, or any internal error text).
var statusPageCopy = map[int]struct{ title, body string }{
	400: {"400 Bad Request", "The request was malformed."},
	401: {"401 Unauthorized", "You need to sign in to view this page."},
	403: {"403 Forbidden", "You do not have access to this page."},
	499: {"499 Client Closed Request", "The request was canceled before it completed."},
	500: {"500 Server Error", "Something went wrong while handling your request."},
	504: {"504 Gateway Timeout", "The request took too long to complete."},
}

// renderHTTPStatusPage renders a status-specific HTML error page for any
// 4xx/5xx code. Unknown codes fall back to a generic 500-style page so a
// response is always produced and the exact status code is always correct.
func renderHTTPStatusPage(c fiber.Ctx, code int) error {
	copy, ok := statusPageCopy[code]
	if !ok {
		copy = statusPageCopy[500]
	}
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Status(code).SendString(
		"<!DOCTYPE html>\n" +
			"<html lang=\"en\">\n" +
			"<head><meta charset=\"utf-8\"><title>" + copy.title + "</title></head>\n" +
			"<body>\n" +
			"<main data-go-error=\"" + strconv.Itoa(code) + "\">\n" +
			"<h1>" + copy.title + "</h1>\n" +
			"<p>" + copy.body + "</p>\n" +
			"<p><a href=\"/\">Go home</a></p>\n" +
			"</main>\n" +
			"</body>\n" +
			"</html>\n")
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
