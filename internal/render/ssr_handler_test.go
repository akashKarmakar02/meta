package render

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// handlerStub implements templ.Component with fixed output, letting the
// handler tests assert exact bytes without touching the templ fixture.
type handlerStub struct{}

func (handlerStub) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, "<p>fixture</p>")
	return err
}

// handlerErrorComponent always fails, exercising error propagation without panicking.
type handlerErrorComponent struct{}

func (handlerErrorComponent) Render(_ context.Context, _ io.Writer) error {
	return io.ErrClosedPipe
}

func TestSSRHandlerWritesStatusHeaderBody(t *testing.T) {
	app := fiber.New()
	app.Get("/page", SSRHandler("/page", handlerStub{}))

	resp, err := app.Test(httptest.NewRequest("GET", "/page", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "<p>fixture</p>" {
		t.Errorf("body = %q, want fixture markup", body)
	}
}

func TestSSRHandlerPropagatesRenderErrorAs500(t *testing.T) {
	app := fiber.New()
	app.Get("/boom", SSRHandler("/boom", handlerErrorComponent{}))

	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500 (render error must propagate)", resp.StatusCode)
	}
}

func TestSSRHandlerServesRealTemplFixture(t *testing.T) {
	app := fiber.New()
	app.Get("/real", SSRHandler("/real", testComponent("fixture")))

	resp, err := app.Test(httptest.NewRequest("GET", "/real", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "fixture") {
		t.Errorf("body missing fixture content: %q", body)
	}
}
