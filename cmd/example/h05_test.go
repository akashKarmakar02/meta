package main

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"go-meta/internal/httpserver"
)

// H05 acceptance: HTTP tests for the versioned navigation JSON protocol,
// SSR and CSR, and invalid versions — "schema asserted". A GET carrying
// X-Go-Web-Navigation: 1 must answer 200 application/json with the full
// v1 envelope; route IDs, URLs, modes, titles, layout ancestry, and the
// mode-specific html/clientEntry rules are all pinned. Unsupported
// versions degrade to normal full documents per the F04/F03 contract
// (blueprint 5.1), never serving an envelope.

// h05Envelope issues a navigation GET for path and decodes the response
// as a NavigationResponse, asserting the shared 200/JSON contract first.
func h05Envelope(t *testing.T, path string) httpserver.NavigationResponse {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set(httpserver.NavigationHeader, "1")
	resp, err := newApp(nil).Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s (nav): status = %d, want 200:\n%s", path, resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET %s (nav): content-type = %q, want application/json", path, ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET %s (nav): cache-control = %q, want no-store", path, cc)
	}
	var env httpserver.NavigationResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("GET %s (nav): invalid envelope JSON: %v", path, err)
	}
	return env
}

// h05AssertSchema pins the wire schema every envelope must satisfy:
// version 1 and non-empty string url/routeId/title, valid mode,
// string-array layouts. Mode-specific rules are asserted by the callers.
func h05AssertSchema(t *testing.T, path string, env httpserver.NavigationResponse) {
	t.Helper()
	if env.Version != httpserver.NavigationVersion {
		t.Errorf("%s: version = %d, want %d", path, env.Version, httpserver.NavigationVersion)
	}
	if env.URL == "" {
		t.Errorf("%s: url is empty", path)
	}
	if env.RouteID == "" {
		t.Errorf("%s: routeId is empty", path)
	}
	if env.Title == "" {
		t.Errorf("%s: title is empty", path)
	}
	for i, l := range env.Layouts {
		if l == "" {
			t.Errorf("%s: layouts[%d] is empty", path, i)
		}
	}
}

// TestH05SSREnvelopesRootAboutDocs: the SSR routes /, /about, and /docs
// answer v1 envelopes: bare page outlet html without enclosing layout or
// document markup, null clientEntry, and the discovered ancestry.
func TestH05SSREnvelopesRootAboutDocs(t *testing.T) {
	// Root: the blueprint's /about example generalizes — every SSR route
	// shares the shape, so the root route asserts it too.
	root := h05Envelope(t, "/")
	h05AssertSchema(t, "/", root)
	if root.URL != "/" || root.RouteID != "page.templ" || root.Mode != "ssr" {
		t.Errorf("/: url/routeId/mode = %q/%q/%q", root.URL, root.RouteID, root.Mode)
	}
	if !strings.Contains(root.HTML, "<h1>go-meta</h1>") {
		t.Errorf("/: html missing root page content: %q", root.HTML)
	}
	if strings.Contains(root.HTML, "<html") || strings.Contains(root.HTML, "<!DOCTYPE") ||
		!strings.Contains(root.HTML, `data-go-layout="app/layout.templ"`) {
		t.Errorf("/: html must contain the visual layout without a document: %q", root.HTML)
	}
	if root.ClientEntry != nil {
		t.Errorf("/: clientEntry = %q, want null for SSR", *root.ClientEntry)
	}

	about := h05Envelope(t, "/about")
	h05AssertSchema(t, "/about", about)
	if about.URL != "/about" || about.RouteID != "about/page.templ" || about.Mode != "ssr" {
		t.Errorf("/about: url/routeId/mode = %q/%q/%q", about.URL, about.RouteID, about.Mode)
	}
	if about.Title != "About" {
		t.Errorf("/about: title = %q, want About", about.Title)
	}
	// The blueprint 5.1 example: envelope html for /about is the bare page.
	wantHTML := "<main data-go-page-about><h1>About</h1><p>go-meta renders templ pages server-side; this one is distinct from the root page.</p></main>"
	if !strings.Contains(about.HTML, wantHTML) || !strings.Contains(about.HTML, `data-go-layout="app/layout.templ"`) {
		t.Errorf("/about: html = %q, want page inside root visual layout", about.HTML)
	}
	if about.ClientEntry != nil {
		t.Errorf("/about: clientEntry = %q, want null for SSR", *about.ClientEntry)
	}
	if len(about.Layouts) != 1 || about.Layouts[0] != "app/layout.templ" {
		t.Errorf("/about: layouts = %v, want [app/layout.templ]", about.Layouts)
	}

	docs := h05Envelope(t, "/docs")
	h05AssertSchema(t, "/docs", docs)
	if docs.URL != "/docs" || docs.RouteID != "docs/page.templ" || docs.Mode != "ssr" {
		t.Errorf("/docs: url/routeId/mode = %q/%q/%q", docs.URL, docs.RouteID, docs.Mode)
	}
	if docs.Title != "Docs" {
		t.Errorf("/docs: title = %q, want Docs", docs.Title)
	}
	if !strings.Contains(docs.HTML, "data-go-page-docs") {
		t.Errorf("/docs: html missing page content: %q", docs.HTML)
	}
	// Bare outlet only: no enclosing layout markup in envelope html —
	// the ordered ancestry travels in `layouts` instead.
	if !strings.Contains(docs.HTML, `data-go-layout="app/docs/layout.templ"`) || strings.Contains(docs.HTML, `data-go-layout="app/layout.templ"`) {
		t.Errorf("/docs: html must include only docs layout: %q", docs.HTML)
	}
	if docs.ClientEntry != nil {
		t.Errorf("/docs: clientEntry = %q, want null for SSR", *docs.ClientEntry)
	}
	if len(docs.Layouts) != 1 ||
		docs.Layouts[0] != "app/docs/layout.templ" {
		t.Errorf("/docs: layouts = %v, want [app/docs/layout.templ]", docs.Layouts)
	}
}

// TestH05CSREnvelopesDashboardSettings: the CSR routes answer v1
// envelopes with empty html (the server never renders Svelte components),
// clientEntry naming the generated import-registry key, and the full
// dashboard ancestry.
func TestH05CSREnvelopesDashboardSettings(t *testing.T) {
	dash := h05Envelope(t, "/dashboard")
	h05AssertSchema(t, "/dashboard", dash)
	if dash.URL != "/dashboard" || dash.RouteID != "dashboard/page.svelte" || dash.Mode != "csr" {
		t.Errorf("/dashboard: url/routeId/mode = %q/%q/%q", dash.URL, dash.RouteID, dash.Mode)
	}
	if dash.Title != "Dashboard" {
		t.Errorf("/dashboard: title = %q, want Dashboard", dash.Title)
	}
	if dash.HTML != "" {
		t.Errorf("/dashboard: html = %q, want exactly \"\" — the browser mounts Svelte client-side", dash.HTML)
	}
	if dash.ClientEntry == nil || *dash.ClientEntry != "dashboard/page.svelte" {
		t.Errorf("/dashboard: clientEntry = %v, want dashboard/page.svelte (registry key)", dash.ClientEntry)
	}
	if len(dash.Layouts) != 1 ||
		dash.Layouts[0] != "app/dashboard/layout.svelte" {
		t.Errorf("/dashboard: layouts = %v, want [app/dashboard/layout.svelte]", dash.Layouts)
	}

	settings := h05Envelope(t, "/dashboard/settings")
	h05AssertSchema(t, "/dashboard/settings", settings)
	if settings.URL != "/dashboard/settings" || settings.RouteID != "dashboard/settings/page.svelte" || settings.Mode != "csr" {
		t.Errorf("/dashboard/settings: url/routeId/mode = %q/%q/%q", settings.URL, settings.RouteID, settings.Mode)
	}
	if settings.Title != "Settings" {
		t.Errorf("/dashboard/settings: title = %q, want Settings", settings.Title)
	}
	if settings.HTML != "" {
		t.Errorf("/dashboard/settings: html = %q, want exactly \"\"", settings.HTML)
	}
	if settings.ClientEntry == nil || *settings.ClientEntry != "dashboard/settings/page.svelte" {
		t.Errorf("/dashboard/settings: clientEntry = %v, want dashboard/settings/page.svelte", settings.ClientEntry)
	}
	if len(settings.Layouts) != 1 ||
		settings.Layouts[0] != "app/dashboard/layout.svelte" {
		t.Errorf("/dashboard/settings: layouts = %v, want [app/dashboard/layout.svelte]", settings.Layouts)
	}

	// Distinct envelopes per route: no metadata bleed between dashboard pages.
	if dash.RouteID == settings.RouteID || dash.ClientEntry == nil || settings.ClientEntry == nil ||
		*dash.ClientEntry == *settings.ClientEntry {
		t.Error("dashboard and settings envelopes are not route-distinct")
	}
}

// TestH05WireShapeMatchesSharedFixtures: the served envelopes match the
// checked-in fixture files both sides pin (Go httpserver + frontend types),
// byte-for-byte after field-order-normalizing unmarshal — server output,
// fixtures, and the client type cannot drift apart.
func TestH05WireShapeMatchesSharedFixtures(t *testing.T) {
	cases := []struct{ path, fixture string }{
		{"/about", "../../tests/fixtures/navigation-ssr.json"},
		{"/dashboard/settings", "../../tests/fixtures/navigation-csr.json"},
	}
	for _, tc := range cases {
		fixtureRaw, err := os.ReadFile(tc.fixture)
		if err != nil {
			t.Fatalf("read fixture %s: %v", tc.fixture, err)
		}
		var want httpserver.NavigationResponse
		if err := json.Unmarshal(fixtureRaw, &want); err != nil {
			t.Fatalf("parse fixture %s: %v", tc.fixture, err)
		}

		env := h05Envelope(t, tc.path)
		if !reflect.DeepEqual(env, want) {
			t.Errorf("%s: served envelope drifts from pinned fixture %s:\ngot  %+v\nwant %+v", tc.path, tc.fixture, env, want)
		}
	}
}

// TestH05InvalidVersionsDegradeToDocuments: header values other than the
// supported version never serve an envelope — the request degrades to the
// ordinary full document on both route modes (blueprint 5.1 / F04
// contract: unsupported version triggers a normal full page navigation).
func TestH05InvalidVersionsDegradeToDocuments(t *testing.T) {
	app := newApp(nil)
	for _, val := range []string{"0", "2", "99", "v1", "", "abc"} {
		for _, path := range []string{"/about", "/dashboard"} {
			req := httptest.NewRequest("GET", path, nil)
			if val != "" {
				req.Header.Set(httpserver.NavigationHeader, val)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Errorf("%s hdr=%q: status = %d, want 200 (degraded document)", path, val, resp.StatusCode)
				continue
			}
			if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
				t.Errorf("%s hdr=%q: content-type = %q, want text/html (degraded document)", path, val, ct)
				continue
			}
			b, _ := io.ReadAll(resp.Body)
			body := string(b)
			if strings.Contains(body, `"routeId"`) || strings.Contains(body, `"clientEntry"`) {
				t.Errorf("%s hdr=%q: invalid version still served an envelope", path, val)
			}
			if !strings.Contains(body, "<body>") {
				t.Errorf("%s hdr=%q: degraded response lost its full document", path, val)
			}
		}
	}
}

// TestH05NormalGETStaysDocument: the envelope is exclusive to the
// versioned header — without it the same routes serve full documents.
func TestH05NormalGETStaysDocument(t *testing.T) {
	for _, path := range []string{"/about", "/dashboard"} {
		req := httptest.NewRequest("GET", path, nil)
		resp, err := newApp(nil).Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s (no header): content-type = %q, want text/html", path, ct)
		}
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), "<body>") {
			t.Errorf("%s (no header): expected a full document", path)
		}
	}
}
