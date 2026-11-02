package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// H03 acceptance: HTTP tests for the full HTML SSR routes / and /about —
// status, MIME type, and content. Each served response must be a complete
// server-rendered HTML document (root layout owns html/head/body, D06)
// whose text is fully present without any browser JavaScript. The
// navigation JSON envelope (F04/H05) is deliberately out of scope: a
// normal GET must never carry it.
//
// The fixture-mode app (nil assets) renders scriptless documents — exactly
// the no-JS contract H03 asserts.

// h03Get fetches a path through the example application without the
// navigation header, failing the test on transport errors.
func h03Get(t *testing.T, path string) (int, string, string) {
	t.Helper()
	resp, err := newApp(nil).Test(httptest.NewRequest("GET", path, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header.Get("Content-Type")
}

// h03AssertDocument verifies the shared full-document contract: doctype
// first, exactly one html/head/body (the root layout owns the document),
// the persistent nav and page outlet present, and no client runtime —
// the content reads without JavaScript.
func h03AssertDocument(t *testing.T, path, body string) {
	t.Helper()
	if !strings.HasPrefix(body, "<!DOCTYPE html>") && !strings.HasPrefix(body, "<!doctype html>") {
		t.Errorf("%s: document must start with doctype, got %q", path, clip(body))
	}
	for _, tag := range []string{"<html", "<head>", "<body>"} {
		if n := strings.Count(body, tag); n != 1 {
			t.Errorf("%s: %d occurrences of %s, want exactly 1 (root layout owns the document)", path, n, tag)
		}
	}
	if !strings.Contains(body, "<title>go-meta</title>") {
		t.Errorf("%s: missing root layout <title>", path)
	}
	if !strings.Contains(body, `<div data-go-layout="app/layout.templ">`) {
		t.Errorf("%s: missing root layout marker", path)
	}
	if !strings.Contains(body, "<nav data-go-site-nav>") {
		t.Errorf("%s: missing site nav above the outlet", path)
	}
	outlet := strings.Index(body, "<nav data-go-site-nav>")
	page := strings.Index(body, "<main data-go-page")
	if outlet == -1 || page == -1 || page < outlet {
		t.Errorf("%s: page markup must render inside the outlet, after the nav", path)
	} else if end := strings.Index(body, "</main>"); end == -1 || end < page {
		t.Errorf("%s: page markup is not a complete <main> element", path)
	}
	// No-JS contract: the SSR document carries no inline or external
	// scripts and no inline event handlers in fixture mode.
	for _, banned := range []string{"<script", "javascript:", " onclick=", " onload="} {
		if strings.Contains(body, banned) {
			t.Errorf("%s: SSR document contains %q; content must render without browser JavaScript", path, banned)
		}
	}
	// Normal GET serves a document, never the navigation envelope (F04).
	if strings.Contains(body, `"routeId"`) || strings.Contains(body, `"version":`) {
		t.Errorf("%s: navigation envelope JSON leaked into a normal document response", path)
	}
}

// TestH03RootServesFullHTMLDocument: GET / answers 200 text/html with the
// complete server-rendered document — the root page inside the root layout.
func TestH03RootServesFullHTMLDocument(t *testing.T) {
	status, body, ctype := h03Get(t, "/")
	if status != 200 {
		t.Fatalf("GET /: status = %d, want 200", status)
	}
	if ctype != "text/html; charset=utf-8" {
		t.Fatalf("GET /: content-type = %q, want text/html; charset=utf-8", ctype)
	}
	h03AssertDocument(t, "/", body)
	for _, want := range []string{
		`<main data-go-page="home">`,
		"<h1>go-meta</h1>",
		"<p>A Go-first full-stack web framework.</p>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /: missing root page content %q", want)
		}
	}
}

// TestH03AboutServesFullHTMLDocument: GET /about answers 200 text/html
// with the complete server-rendered document — distinct content from /.
func TestH03AboutServesFullHTMLDocument(t *testing.T) {
	status, body, ctype := h03Get(t, "/about")
	if status != 200 {
		t.Fatalf("GET /about: status = %d, want 200", status)
	}
	if ctype != "text/html; charset=utf-8" {
		t.Fatalf("GET /about: content-type = %q, want text/html; charset=utf-8", ctype)
	}
	h03AssertDocument(t, "/about", body)
	for _, want := range []string{
		"<main data-go-page-about>",
		"<h1>About</h1>",
		"<p>go-meta renders templ pages server-side; this one is distinct from the root page.</p>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /about: missing about page content %q", want)
		}
	}
	// The two SSR pages are distinct documents, not one shared body.
	_, rootBody, _ := h03Get(t, "/")
	if strings.Contains(rootBody, "<h1>About</h1>") {
		t.Error("root page carries about page content; SSR pages must be distinct")
	}
}

// TestH03PagesDistinctPerRoute: each registered SSR route serves its own
// document; route selection cannot collapse onto a default handler.
func TestH03PagesDistinctPerRoute(t *testing.T) {
	app := newApp(nil)
	bodies := make(map[string]string)
	for _, path := range []string{"/", "/about"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		bodies[path] = string(b)
	}
	if bodies["/"] == bodies["/about"] {
		t.Error("/ and /about served byte-identical bodies")
	}
}

// TestH03UnknownPathServesCentralized404: a normal GET to an unregistered
// path answers 404 as an HTML document from the centralized error handler
// (D07) — safe messages only, no internals or stack traces.
func TestH03UnknownPathServesCentralized404(t *testing.T) {
	status, body, ctype := h03Get(t, "/definitely-not-a-page")
	if status != 404 {
		t.Fatalf("GET /definitely-not-a-page: status = %d, want 404", status)
	}
	if !strings.HasPrefix(ctype, "text/html") {
		t.Fatalf("GET /definitely-not-a-page: content-type = %q, want text/html", ctype)
	}
	if !strings.Contains(body, `data-go-error="404"`) {
		t.Errorf("404 body missing centralized error page marker:\n%s", body)
	}
	for _, leaked := range []string{"goroutine", ".go:", "panic"} {
		if strings.Contains(body, leaked) {
			t.Errorf("404 body leaks internals (%q found)", leaked)
		}
	}
}

// TestH03NonGETMethodsRejected: the SSR document routes are GET-only;
// state-changing methods answer 405 with the centralized error page.
func TestH03NonGETMethodsRejected(t *testing.T) {
	app := newApp(nil)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/about", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 405 {
			t.Errorf("%s /about: status = %d, want 405", method, resp.StatusCode)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		body := string(b)
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s /about: content-type = %q, want text/html", method, resp.Header.Get("Content-Type"))
		}
		if !strings.Contains(body, `data-go-error="405"`) {
			t.Errorf("%s /about: missing centralized 405 page:\n%s", method, body)
		}
		if strings.Contains(body, `"routeId"`) {
			t.Errorf("%s /about: envelope JSON served for non-GET", method)
		}
	}
}
