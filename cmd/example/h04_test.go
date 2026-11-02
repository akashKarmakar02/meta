package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	routes "go-meta/generated"
	"go-meta/internal/httpserver"
	"go-meta/internal/routing"
)

// H04 acceptance: HTTP tests for the full CSR shells /dashboard and
// /dashboard/settings — "scripts/mount present". Each served response is
// the boot shell the browser mounts Svelte into on direct navigation or
// reload: a complete HTML document whose only body markup is the single
// mount root carrying route metadata. The server never renders Svelte page
// content — that is the client runtime's job.
//
// Two asset modes exist and both are asserted:
//
//   - fixture mode (nil assets): scriptless shell — proves the shell
//     renders without any manifest at all;
//   - production mode (manifest-resolved Assets): the Vite entry module
//     script and entry CSS links are injected, which is H04's
//     "scripts present" half.
//
// Soft-navigation envelopes (H05) are out of scope: only normal GETs here.

// h04CSRRoute returns the generated manifest entry for a route path,
// proving ancestry comes from discovery/codegen, not the HTTP layer.
func h04CSRRoute(t *testing.T, path string) routing.Route {
	t.Helper()
	for _, r := range routes.RegisterRoutes() {
		if r.Path == path {
			return r
		}
	}
	t.Fatalf("route %s missing from generated manifest", path)
	return routing.Route{}
}

// h04AssertShell verifies the shared CSR shell contract for one route:
// complete document, exactly one mount root with that route's metadata,
// and no pre-rendered Svelte page markup. Returns the body.
func h04AssertShell(t *testing.T, app *fiber.App, path, routeID string) string {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", path, nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("%s: status = %d, want 200", path, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("%s: content-type = %q, want text/html; charset=utf-8", path, ct)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)

	if !strings.HasPrefix(body, "<!DOCTYPE html>") && !strings.HasPrefix(body, "<!doctype html>") {
		t.Errorf("%s: shell must start with doctype, got %q", path, clip(body))
	}
	for _, tag := range []string{"<html", "<head>", "<body>"} {
		if n := strings.Count(body, tag); n != 1 {
			t.Errorf("%s: %d occurrences of %s, want exactly 1", path, n, tag)
		}
	}

	// Mount root: exactly one #app element carrying the route metadata.
	mount := `<div id="app" data-go-page-root`
	if n := strings.Count(body, mount); n != 1 {
		t.Errorf("%s: %d mount roots, want exactly 1:\n%s", path, n, body)
	}
	if !strings.Contains(body, `data-go-route="`+path+`"`) {
		t.Errorf("%s: mount root missing data-go-route=%q", path, path)
	}
	if !strings.Contains(body, `data-go-route-id="`+routeID+`"`) {
		t.Errorf("%s: mount root missing data-go-route-id=%q", path, routeID)
	}

	// The server does not render Svelte components: no page body markup
	// from either dashboard page may appear in the shell.
	for _, banned := range []string{
		"<h1>Dashboard</h1>",
		"<h1>Settings</h1>",
		"Count:",
		"Increment",
		"Decrement",
		"Save settings",
		"<main data-go-page",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("%s: shell carries pre-rendered Svelte content %q; the browser mounts pages client-side", path, banned)
		}
	}
	return body
}

// TestH04FixtureShellDocuments: without assets, both CSR routes serve
// complete scriptless shells with correct mount metadata — the shell
// renders without any manifest.
func TestH04FixtureShellDocuments(t *testing.T) {
	app := newApp(nil)
	dashBody := h04AssertShell(t, app, "/dashboard", "dashboard/page.svelte")
	setBody := h04AssertShell(t, app, "/dashboard/settings", "dashboard/settings/page.svelte")
	if dashBody == setBody {
		t.Error("/dashboard and /dashboard/settings served identical shells")
	}
	for _, body := range []string{dashBody, setBody} {
		if strings.Contains(body, "<script") {
			t.Error("fixture-mode shell must be scriptless (no manifest resolved)")
		}
	}
}

// TestH04ProductionShellInjectsScriptsAndKeepsMount: with manifest-resolved
// assets, both shells inject the entry module script and entry CSS while
// keeping exactly one unchanged mount root — H04's scripts/mount contract.
func TestH04ProductionShellInjectsScriptsAndKeepsMount(t *testing.T) {
	assets := &httpserver.Assets{
		EntryScriptURL: "/assets/entry-H04TEST.js",
		EntryCSSURLs:   []string{"/assets/entry-H04TEST.css"},
	}
	app := newApp(assets)
	for _, tc := range []struct{ path, id string }{
		{"/dashboard", "dashboard/page.svelte"},
		{"/dashboard/settings", "dashboard/settings/page.svelte"},
	} {
		body := h04AssertShell(t, app, tc.path, tc.id)

		wantScript := `<script type="module" src="/assets/entry-H04TEST.js"></script>`
		if n := strings.Count(body, wantScript); n != 1 {
			t.Errorf("%s: %d entry script tags, want exactly 1:\n%s", tc.path, n, body)
		}
		if !strings.Contains(body, `<link rel="stylesheet" href="/assets/entry-H04TEST.css">`) {
			t.Errorf("%s: shell missing entry CSS link:\n%s", tc.path, body)
		}
		// The injected script sits inside the body, after the mount root,
		// so the element exists before the module executes.
		mountIdx := strings.Index(body, `<div id="app"`)
		scriptIdx := strings.Index(body, wantScript)
		bodyClose := strings.Index(body, "</body>")
		if mountIdx == -1 || scriptIdx == -1 || bodyClose == -1 ||
			!(mountIdx < scriptIdx && scriptIdx < bodyClose) {
			t.Errorf("%s: entry script must follow the mount root inside body", tc.path)
		}
	}
}

// TestH04LayoutAncestryFromManifest: the dashboard routes' layout chains
// come from route discovery and codegen (generated manifest + TS registry),
// never fabricated by the HTTP handlers. The Go side declares the chain;
// the shell itself stays mode-agnostic and the client resolves mounting.
func TestH04LayoutAncestryFromManifest(t *testing.T) {
	want := []string{"app/layout.templ", "app/dashboard/layout.svelte"}
	for _, path := range []string{"/dashboard", "/dashboard/settings"} {
		r := h04CSRRoute(t, path)
		if r.Mode != routing.ModeCSR {
			t.Errorf("%s: mode = %q, want csr", path, r.Mode)
		}
		got := make([]string, 0, len(r.Layouts))
		for _, l := range r.Layouts {
			got = append(got, l.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: layout ancestry = %v, want %v (discovery order, outermost first)", path, got, want)
		}
	}
}

// TestH04SSRRoutesUnaffected: the CSR shell contract did not disturb the
// SSR document routes — /about still answers its composed full document.
func TestH04SSRRoutesUnaffected(t *testing.T) {
	resp, err := newApp(nil).Test(httptest.NewRequest("GET", "/about", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET /about: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("GET /about: content-type = %q", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "<h1>About</h1>") {
		t.Error("GET /about lost its SSR content")
	}
}

// TestH04NonGETMethodsRejected: CSR routes are GET-only like SSR routes;
// other methods answer 405 with the centralized error page.
func TestH04NonGETMethodsRejected(t *testing.T) {
	app := newApp(nil)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/dashboard", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 405 {
			t.Errorf("%s /dashboard: status = %d, want 405", method, resp.StatusCode)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), `data-go-error="405"`) {
			t.Errorf("%s /dashboard: missing centralized 405 page:\n%s", method, b)
		}
	}
}
