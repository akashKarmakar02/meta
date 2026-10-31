package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-meta/internal/routing"
)

// newTestApp wires a handler into a bare Fiber app for handler tests.
func newTestApp(h fiber.Handler) *fiber.App {
	app := fiber.New()
	app.Get("/dashboard", h)
	return app
}

func fixtureRoute() routing.Route {
	r, err := routing.NewRoute("/dashboard", "dashboard/page.svelte", routing.ModeCSR, nil)
	if err != nil {
		panic(err)
	}
	return r
}

// TestShellDocument: without assets (pre-E08 test mode) the shell is a
// valid HTML5 document with the mount element and route metadata
// attributes.
func TestShellDocument(t *testing.T) {
	got := Shell(fixtureRoute(), nil)
	for _, want := range []string{
		"<!DOCTYPE html>",
		`<div id="app" data-go-page-root data-go-route="/dashboard" data-go-route-id="dashboard/page.svelte">`,
		"</html>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("shell missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "<!DOCTYPE html>") {
		t.Error("shell must start with doctype")
	}
}

// TestShellEscapesRouteMetadata: hostile route metadata can never inject
// markup — the escaping contract is part of the stable shell structure.
func TestShellEscapesRouteMetadata(t *testing.T) {
	r, err := routing.NewRoute("/x\"><script>", "x/page.svelte", routing.ModeCSR, nil)
	if err != nil {
		t.Fatalf("NewRoute: %v", err)
	}
	got := Shell(r, nil)
	if strings.Contains(got, "<script>") {
		t.Errorf("unescaped route data in shell:\n%s", got)
	}
	for _, want := range []string{"&#34;", "&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected escaped entities %q in shell", want)
		}
	}
}

// TestShellHandlerResponse: the handler returns 200 HTML with the shell.
func TestShellHandlerResponse(t *testing.T) {
	app := newTestApp(ShellHandler(fixtureRoute(), nil))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q, want text/html; charset=utf-8", ct)
	}
}
