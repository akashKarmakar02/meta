package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go-meta/generated"
)

// --- existing tests (restored) ---

func TestHealthz(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/healthz", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Fatalf("body = %q", b)
	}
}

func TestUnknownRoute404(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/nope", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPanicReturns500(t *testing.T) {
	app := newApp()
	app.Get("/boom", func(c fiber.Ctx) error {
		panic("boom")
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestSSRStillRender(t *testing.T) {
	app := newApp()
	for path, marker := range map[string]string{
		"/":      "go-meta",
		"/about": "About",
	} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status = %d", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("%s: content-type = %q", path, got)
		}
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), marker) {
			t.Fatalf("%s: missing %q in %q", path, marker, b)
		}
	}
}

func TestAboutTextPresentWithoutJS(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/about", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, "<h1>About</h1>") {
		t.Fatalf("missing about heading: %q", body)
	}
	for _, banned := range []string{"<script", "javascript:", " onclick=", " onload="} {
		if strings.Contains(body, banned) {
			t.Fatalf("about page must render without JS; found %q", banned)
		}
	}
}

func TestCSRShellServed(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/dashboard", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q", got)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	for _, want := range []string{
		`<div id="app"`,
		`data-go-route="/dashboard"`,
		`data-go-route-id="dashboard/page.svelte"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<script") {
		t.Fatalf("C06 shell must not embed scripts yet: %q", body)
	}
}

func TestNonGETMethodsRejectPageRoutes(t *testing.T) {
	app := newApp()
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/about", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 405 {
			t.Fatalf("%s /about: status = %d, want 405", method, resp.StatusCode)
		}
	}
}

func TestUnknownRoute404Body(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/missing", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "data-go-error=\"404\"") {
		t.Fatalf("404 body missing centralized error page: %q", b)
	}
	if strings.Contains(string(b), "goroutine") || strings.Contains(string(b), ".go:") {
		t.Fatalf("404 leaked stack trace: %q", b)
	}
}

func TestPageRoutesKeepServingAfterNotFound(t *testing.T) {
	app := newApp()
	if _, err := app.Test(httptest.NewRequest("GET", "/nope", nil)); err != nil {
		t.Fatal(err)
	}
	resp, err := app.Test(httptest.NewRequest("GET", "/healthz", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("healthz after 404: %d", resp.StatusCode)
	}
}

func TestGeneratedRouteCount(t *testing.T) {
	rs := routes.RegisterRoutes()
	want := len(rs)
	if want == 0 {
		t.Fatal("generated route data missing")
	}
	var ssr, csr int
	for _, r := range rs {
		switch r.Mode {
		case "ssr":
			ssr++
		case "csr":
			csr++
		}
	}
	if ssr == 0 || csr == 0 {
		t.Fatalf("expected both SSR and CSR routes, got ssr=%d csr=%d", ssr, csr)
	}
}

// --- D06 layout composition tests ---

func TestRootPageInsideRootLayout(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.HasPrefix(body, "<!DOCTYPE html>") && !strings.HasPrefix(body, "<!doctype html>") {
		t.Fatalf("root page must be wrapped in full document layout: %q", body[:min(len(body), 80)])
	}
	if !strings.Contains(body, "<html") || !strings.Contains(body, "<body>") {
		t.Fatalf("layout document structure missing: %q", body)
	}
	if !strings.Contains(body, "<h1>go-meta</h1>") {
		t.Fatalf("page content missing inside layout: %q", body)
	}
}

func TestAboutInsideRootLayout(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/about", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, "<html") || !strings.Contains(body, "<body>") {
		t.Fatalf("root layout missing around about page: %q", body)
	}
	if !strings.Contains(body, "<h1>About</h1>") {
		t.Fatalf("about content missing: %q", body)
	}
}

func TestExactlyOneDocumentRoot(t *testing.T) {
	app := newApp()
	for _, path := range []string{"/", "/about", "/docs"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		body := string(b)
		if n := strings.Count(body, "<html"); n != 1 {
			t.Fatalf("%s: %d <html> roots, want exactly 1", path, n)
		}
		if n := strings.Count(body, "<body>"); n != 1 {
			t.Fatalf("%s: %d <body> tags, want exactly 1", path, n)
		}
	}
}

func TestNestedLayoutComposition(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/docs", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	// Outermost first: root layout document
	if !strings.Contains(body, "<body>") {
		t.Fatalf("root layout missing: %q", body)
	}
	// Nested docs layout inside root
	rootIdx := strings.Index(body, "<body>")
	docsIdx := strings.Index(body, "data-go-layout-docs")
	if docsIdx == -1 {
		t.Fatalf("docs layout marker missing: %q", body)
	}
	if docsIdx < rootIdx {
		t.Fatalf("docs layout must nest inside root layout body")
	}
	// Page innermost
	pageIdx := strings.LastIndex(body, "data-go-page-docs")
	if pageIdx == -1 || pageIdx < docsIdx {
		t.Fatalf("docs page must render innermost: %q", body)
	}
}

func TestSiblingLayoutsDoNotLeak(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/about", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "docs-navigation") {
		t.Fatalf("docs layout leaked into /about")
	}
}

func TestLayoutAppliedOnce(t *testing.T) {
	app := newApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/docs", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if n := strings.Count(string(b), "data-go-layout-docs"); n != 1 {
		t.Fatalf("docs layout applied %d times, want 1", n)
	}
	if n := strings.Count(string(b), "<h1>go-meta</h1>"); n > 0 {
		t.Fatalf("unexpected root page content on /docs: %q", b)
	}
}
