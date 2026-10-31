package render

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestHTTPErrorHandler404(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: HTTPErrorHandler})
	req := httptest.NewRequest("GET", "/definitely-missing", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	t.Logf("status=%d body=%q", resp.StatusCode, string(body))
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("content type = %q, want text/html", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), "404") || !strings.Contains(string(body), "data-go-error=\"404\"") {
		t.Errorf("body missing 404 page markers: %q", body)
	}
}

func TestHTTPErrorHandler405(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: HTTPErrorHandler})
	app.Get("/only-get", func(c fiber.Ctx) error { return nil })
	req := httptest.NewRequest("POST", "/only-get", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 405 {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
	if !strings.Contains(string(body), "405") || !strings.Contains(string(body), "data-go-error=\"405\"") {
		t.Errorf("body missing 405 page markers: %q", body)
	}
	if !strings.Contains(string(body), "POST") {
		t.Errorf("body should name the rejected method: %q", body)
	}
}

func TestHTTPErrorHandler500HidesInternals(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: HTTPErrorHandler})
	app.Get("/boom", func(c fiber.Ctx) error {
		return errors.New("database password is hunter2")
	})
	req := httptest.NewRequest("GET", "/boom", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if strings.Contains(string(body), "hunter2") {
		t.Error("internal error detail leaked into response body")
	}
	if !strings.Contains(string(body), "data-go-error=\"500\"") {
		t.Errorf("body missing 500 page markers: %q", body)
	}
}

func TestHTTPErrorHandlerUniqueStatuses(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{"/missing", 404},
		{"/boom", 500},
	}
	for _, tc := range cases {
		app := fiber.New(fiber.Config{ErrorHandler: HTTPErrorHandler})
		if tc.path == "/boom" {
			app.Get("/boom", func(c fiber.Ctx) error { return errors.New("kaboom") })
		}
		req := httptest.NewRequest("GET", tc.path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
		if resp.Header.Get("Content-Type") == "" {
			t.Errorf("%s: missing content type", tc.path)
		}
	}
}
