package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestHealthz(t *testing.T) {
	app := newApp()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/healthz", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q, want text/plain; charset=utf-8", ct)
	}

	body := make([]byte, resp.ContentLength)
	if _, err := resp.Body.Read(body); err != nil && err.Error() != "EOF" {
		t.Fatalf("reading body: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", string(body), "ok")
	}
}

func TestUnknownRoute404(t *testing.T) {
	app := newApp()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/nope", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
}

// TestPanicReturns500 asserts that recover middleware converts a handler
// panic into a 500 response without killing the app.
func TestPanicReturns500(t *testing.T) {
	app := newApp()

	app.Get("/boom", func(_ fiber.Ctx) error {
		panic("kaboom")
	})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/boom", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusInternalServerError)
	}

	// Server must survive the panic: the next request is still served.
	resp2, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/healthz", nil))
	if err != nil {
		t.Fatalf("request after panic failed: %v", err)
	}
	if resp2.StatusCode != fiber.StatusOK {
		t.Errorf("status after panic = %d, want %d", resp2.StatusCode, fiber.StatusOK)
	}
}
