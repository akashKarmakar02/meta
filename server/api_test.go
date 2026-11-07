package server

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestMountAPI(t *testing.T) {
	app := fiber.New()
	MountAPI(app, APIRoute{Path: "/api/posts/:id", Handlers: map[string]fiber.Handler{
		"GET": func(c fiber.Ctx) error { return c.SendString(c.Params("id")) },
	}})
	response, err := app.Test(httptest.NewRequest("GET", "/api/posts/42", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || string(body) != "42" {
		t.Fatalf("GET: %d %q", response.StatusCode, body)
	}
	response, err = app.Test(httptest.NewRequest("DELETE", "/api/posts/42", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 405 || response.Header.Get("Allow") != "GET" {
		t.Fatalf("DELETE: %d %q", response.StatusCode, response.Header.Get("Allow"))
	}
}
