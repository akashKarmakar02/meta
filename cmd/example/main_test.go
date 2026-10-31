package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	routes "go-meta/generated"
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

// TestGeneratedRoutesRespond is the C05 acceptance fixture: both generated
// routes answer through Fiber with their own SSR pages; unknown routes 404;
// /healthz is untouched.
func TestGeneratedRoutesRespond(t *testing.T) {
	app := newApp()

	for path, wantBody := range map[string]string{
		"/":      "<main data-go-page",
		"/about": "<main data-go-page-about>",
	} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), wantBody) {
			t.Errorf("GET %s body = %q, missing %q", path, body, wantBody)
		}
	}

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/nope", nil))
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /nope status = %d, want 404", resp.StatusCode)
	}
}

// TestDashboardCSRShell: /dashboard is a CSR route — the response must be
// the HTML shell: 200, HTML content type, mount element, and route
// metadata the Phase E runtime will boot from.
func TestDashboardCSRShell(t *testing.T) {
	app := newApp()
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
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		`<div id="app" data-go-route="/dashboard" data-go-route-id="dashboard/page.svelte">`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("response missing %q:\n%s", want, body)
		}
	}
}

// TestSSRStillRender: SSR routes render their own compiled component, not
// a shell — distinct content per route proves per-route symbols.
func TestSSRStillRender(t *testing.T) {
	app := newApp()
	for path, want := range map[string]string{
		"/":      `<main data-go-page-root>Welcome</main>`,
		"/about": `<main data-go-page-about>About</main>`,
	} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("%s: test request: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), want) {
			t.Errorf("%s: missing %q in:\n%s", path, want, body)
		}
	}
}

// TestRoutesRegisteredExactlyOnce: registration cannot duplicate routes.
// Duplicates would require duplicate paths in the generated descriptor set
// (B08 rejects those at discovery), and the registration loop is a single
// pass. Verified two ways: descriptor paths are unique, and /about — a
// path that exists only as generated output, not as middleware (Fiber
// middleware mounts appear in Stack() as "/" entries) — occurs exactly
// once in the GET stack.
func TestRoutesRegisteredExactlyOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range routes.RegisterRoutes() {
		if seen[r.Path] {
			t.Errorf("duplicate path %q in RegisterRoutes()", r.Path)
		}
		seen[r.Path] = true
	}

	app := newApp()
	count := 0
	for _, entry := range app.Stack()[0] { // stack[0] is GET
		if entry.Path == "/about" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("GET stack contains %d /about entries, want exactly 1", count)
	}
}

// TestGeneratedRouteCount: the app registers exactly the routes the
// generator emitted (3 in this tree) — deterministic, no drift.
func TestGeneratedRouteCount(t *testing.T) {
	if got := len(routes.RegisterRoutes()); got != 3 {
		t.Errorf("RegisterRoutes() = %d routes, want 3", got)
	}
}

// TestNonGETMethodsRejectPageRoutes is the C07 method-restriction fixture:
// page routes are GET-only resources. A POST to any page route must never
// produce SSR HTML or a CSR shell — Fiber answers 405 Method Not Allowed
// and leaves the GET response untouched.
func TestNonGETMethodsRejectPageRoutes(t *testing.T) {
	app := newApp()

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	paths := []string{"/", "/about", "/dashboard"} // SSR, SSR, CSR shells alike

	for _, p := range paths {
		for _, m := range methods {
			resp, err := app.Test(httptest.NewRequest(m, p, nil))
			if err != nil {
				t.Fatalf("%s %s: request failed: %v", m, p, err)
			}
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s status = %d, want %d (page output must be GET-only)", m, p, resp.StatusCode, http.StatusMethodNotAllowed)
			}
		}
	}

	// HEAD is intentionally served by the GET handler (RFC semantics:
	// same headers, no body). Assert it works rather than rejecting it.
	resp, err := app.Test(httptest.NewRequest(http.MethodHead, "/about", nil))
	if err != nil {
		t.Fatalf("HEAD /about: request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD /about status = %d, want 200", resp.StatusCode)
	}
}

// TestUnknownRoute404Body asserts the 404 fallback: unknown paths return
// the standard Fiber 404 without crashing the app or leaking page output,
// and the app keeps serving after a 404.
func TestUnknownRoute404Body(t *testing.T) {
	app := newApp()

	for _, p := range []string{"/missing", "/nope", "/dashboard/extra"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, p, nil))
		if err != nil {
			t.Fatalf("GET %s: request failed: %v", p, err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", p, resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "<main data-go-page") {
			t.Errorf("GET %s body leaked page content:\n%s", p, body)
		}
	}

	// App still healthy after the 404s.
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if err != nil {
		t.Fatalf("GET /healthz after 404s: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz after 404s status = %d, want 200", resp.StatusCode)
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
