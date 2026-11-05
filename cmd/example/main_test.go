package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akashKarmakar02/meta/generated"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/gofiber/fiber/v3"
)

// --- existing tests (restored) ---

func TestHealthz(t *testing.T) {
	app := newApp(nil)
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

func TestPrivateDevProxyClientIP(t *testing.T) {
	for _, private := range []bool{false, true} {
		app := newAppWithDevProxy(nil, private)
		cfg := app.Config()
		if cfg.TrustProxy != private || cfg.TrustProxyConfig.UnixSocket != private {
			t.Fatalf("private=%t, trust config=%+v", private, cfg.TrustProxyConfig)
		}
		app.Get("/client-ip", func(c fiber.Ctx) error { return c.SendString(c.IP()) })
		req := httptest.NewRequest("GET", "/client-ip", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.42")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		// The test request is not from the trusted private Unix socket.
		if string(body) == "203.0.113.42" {
			t.Errorf("private=%t accepted spoofed IP", private)
		}
	}
}

func TestUnknownRoute404(t *testing.T) {
	app := newApp(nil)
	resp, err := app.Test(httptest.NewRequest("GET", "/nope", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPanicReturns500(t *testing.T) {
	app := newApp(nil)
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
	app := newApp(nil)
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

// F12: with manifest-resolved assets, SSR pages embed the client entry
// before </body> so the soft-navigation pipeline boots on server-rendered
// documents too; without assets (fixture mode) they stay scriptless.
func TestSSRInjectsEntryScriptWhenAssetsPresent(t *testing.T) {
	assets := httpserver.Assets{EntryScriptURL: "/assets/entry-TEST.js"}
	app := newApp(&assets)
	for _, path := range []string{"/", "/about", "/docs"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status = %d", path, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		body := string(b)
		want := `<script type="module" src="/assets/entry-TEST.js"></script></body>`
		if !strings.Contains(body, want) {
			t.Fatalf("%s: SSR page missing client entry before </body>:\n%s", path, body)
		}
	}

	scriptless := newApp(nil)
	resp, err := scriptless.Test(httptest.NewRequest("GET", "/about", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "<script") {
		t.Fatalf("about page without assets must stay scriptless:\n%s", b)
	}
}

func TestAboutTextPresentWithoutJS(t *testing.T) {
	app := newApp(nil)
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
	app := newApp(nil)
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
		t.Fatalf("shell without assets must not embed scripts: %q", body)
	}
}

// E08: with manifest-resolved assets, the CSR shell injects the entry
// module script; Dashboard and Settings shells both reference it.
func TestCSRShellInjectsEntryScript(t *testing.T) {
	assets := httpserver.Assets{
		EntryScriptURL: "/assets/entry-TESTHASH.js",
	}
	app := newApp(&assets)
	for _, path := range []string{"/dashboard", "/dashboard/settings"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status = %d", path, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		body := string(b)
		want := `<script type="module" src="/assets/entry-TESTHASH.js"></script>`
		if !strings.Contains(body, want) {
			t.Fatalf("%s: shell missing entry script %q:\n%s", path, want, body)
		}
		// Mount element and route metadata unchanged by injection.
		id := "dashboard/page.svelte"
		if path == "/dashboard/settings" {
			id = "dashboard/settings/page.svelte"
		}
		if !strings.Contains(body, `data-go-route-id="`+id+`"`) {
			t.Fatalf("%s: route id metadata changed:\n%s", path, body)
		}
		if !strings.Contains(body, `<div id="app"`) {
			t.Fatalf("%s: mount element missing:\n%s", path, body)
		}
	}
}

// E08: entry CSS from the manifest is injected as a stylesheet link.
func TestCSRShellInjectsEntryCSS(t *testing.T) {
	assets := httpserver.Assets{
		EntryScriptURL: "/assets/entry-X.js",
		EntryCSSURLs:   []string{"/assets/entry-X.css"},
	}
	app := newApp(&assets)
	resp, err := app.Test(httptest.NewRequest("GET", "/dashboard", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, `<link rel="stylesheet" href="/assets/entry-X.css">`) {
		t.Fatalf("shell missing CSS link:\n%s", body)
	}
}

func TestNonGETMethodsRejectPageRoutes(t *testing.T) {
	app := newApp(nil)
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
	app := newApp(nil)
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
	app := newApp(nil)
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
	app := newApp(nil)
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
	app := newApp(nil)
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
	app := newApp(nil)
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
	app := newApp(nil)
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

// TestDocsWrapperOrder is D08's acceptance fixture: the composed
// wrapper order must be root > docs > page, each exactly once.
func TestDocsWrapperOrder(t *testing.T) {
	app := newApp(nil)
	resp, err := app.Test(httptest.NewRequest("GET", "/docs", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	// Root layout owns the document: exactly one <html>, <head>, <body>.
	if n := strings.Count(body, "<html"); n != 1 {
		t.Fatalf("root layout: %d <html> elements, want 1", n)
	}
	if n := strings.Count(body, "<body>"); n != 1 {
		t.Fatalf("root layout: %d <body> elements, want 1", n)
	}

	// Strict ordering: root(document) > docs layout > page.
	rootIdx := strings.Index(body, "<body>")
	docsIdx := strings.Index(body, "data-go-layout-docs")
	pageIdx := strings.Index(body, "data-go-page-docs")
	if rootIdx == -1 || docsIdx == -1 || pageIdx == -1 {
		t.Fatalf("missing composition markers in: %q", body)
	}
	if !(rootIdx < docsIdx && docsIdx < pageIdx) {
		t.Fatalf("wrapper order root(%d) > docs(%d) > page(%d) violated", rootIdx, docsIdx, pageIdx)
	}

	// No duplication: each layer appears exactly once.
	if n := strings.Count(body, "data-go-layout-docs"); n != 1 {
		t.Fatalf("docs layout applied %d times, want 1", n)
	}
	if n := strings.Count(body, "data-go-page-docs"); n != 1 {
		t.Fatalf("docs page rendered %d times, want 1", n)
	}
}

func TestSiblingLayoutsDoNotLeak(t *testing.T) {
	app := newApp(nil)
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
	app := newApp(nil)
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
