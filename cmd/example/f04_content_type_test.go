package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	routes "github.com/akashKarmakar02/meta/generated"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
)

// F04: the response-shape contract holds on every registered page route,
// both directions:
//
//   - a normal GET (no navigation header) ALWAYS receives an HTML document;
//   - a special request (X-Go-Web-Navigation: 1) ALWAYS receives
//     application/json — never a document;
//   - an unsupported header version degrades to the normal document
//     identically on SSR and CSR routes (blueprint 5.1 version check).
//
// The matrix iterates the generated manifest rather than a hand-typed list,
// so new routes cannot dodge the contract by omission.

// concretePath materializes a routable URL for a (possibly dynamic) route
// path, substituting :params with sample values.
func concretePath(pattern string) string {
	segs := strings.Split(pattern, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			segs[i] = "42"
		}
	}
	return strings.Join(segs, "/")
}

// routeCases builds the full route table with mode and expected page marker.
func routeCases(t *testing.T) []struct {
	path   string
	mode   routing.RenderMode
	marker string
} {
	t.Helper()
	var cases []struct {
		path   string
		mode   routing.RenderMode
		marker string
	}
	for _, r := range routes.RegisterRoutes() {
		marker := "data-go-page-root" // CSR shell mount point
		if r.Mode == routing.ModeSSR {
			marker = "<body>" // SSR full document layout
		}
		cases = append(cases, struct {
			path   string
			mode   routing.RenderMode
			marker string
		}{concretePath(r.Path), r.Mode, marker})
	}
	if len(cases) == 0 {
		t.Fatal("generated route manifest is empty")
	}
	return cases
}

// doGet issues a GET with an optional navigation header value, returning
// the response and body.
func doGet(t *testing.T, app *fiber.App, path, headerValue string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if headerValue != "" {
		req.Header.Set(httpserver.NavigationHeader, headerValue)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// TestF04NormalGETAlwaysReturnsDocument: without the navigation header,
// every page route answers text/html with a full document — SSR composed
// pages and CSR boot shells alike.
func TestF04NormalGETAlwaysReturnsDocument(t *testing.T) {
	app := newApp(nil)
	for _, tc := range routeCases(t) {
		resp, body := doGet(t, app, tc.path, "")
		if resp.StatusCode != 200 {
			t.Errorf("%s: status = %d, want 200", tc.path, resp.StatusCode)
			continue
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: content type = %q, want text/html", tc.path, ct)
		}
		if !strings.Contains(strings.ToLower(body), "<!doctype") {
			t.Errorf("%s: no doctype — not a full document", tc.path)
		}
		if !strings.Contains(body, tc.marker) {
			t.Errorf("%s: missing %s marker for mode %s", tc.path, tc.marker, tc.mode)
		}
		if strings.Contains(body, `"version":1`) || strings.Contains(body, `"routeId"`) {
			t.Errorf("%s: navigation envelope leaked into normal GET", tc.path)
		}
	}
}

// TestF04SpecialRequestAlwaysJSON: with the supported header version,
// every page route answers application/json — never a document.
func TestF04SpecialRequestAlwaysJSON(t *testing.T) {
	app := newApp(nil)
	for _, tc := range routeCases(t) {
		resp, body := doGet(t, app, tc.path, "1")
		if resp.StatusCode != 200 {
			t.Errorf("%s: status = %d, want 200", tc.path, resp.StatusCode)
			continue
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: content type = %q, want application/json", tc.path, ct)
			continue
		}
		var env httpserver.NavigationResponse
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Errorf("%s: invalid envelope JSON: %v\nbody: %s", tc.path, err, clip(body))
			continue
		}
		if env.Version != httpserver.NavigationVersion {
			t.Errorf("%s: version = %d, want %d", tc.path, env.Version, httpserver.NavigationVersion)
		}
		if want := string(tc.mode); env.Mode != want {
			t.Errorf("%s: mode = %q, want %q", tc.path, env.Mode, want)
		}
		if strings.Contains(body, "<!doctype") && strings.Contains(body, "<html") {
			t.Errorf("%s: envelope carries a full document", tc.path)
		}
	}
}

// TestF04UnsupportedVersionDegradesConsistently: header values other than
// the supported version fall back to the ordinary document on BOTH route
// modes — the regression test for the version-negotiation policy
// (blueprint 5.1: unsupported version triggers a normal full-page
// navigation, not an error and not an unversioned envelope).
func TestF04UnsupportedVersionDegradesConsistently(t *testing.T) {
	app := newApp(nil)
	for _, val := range []string{"0", "2", "10", "v1", "abc"} {
		for _, tc := range routeCases(t) {
			resp, body := doGet(t, app, tc.path, val)
			if resp.StatusCode != 200 {
				t.Errorf("%s hdr=%q: status = %d, want 200 (degraded document)", tc.path, val, resp.StatusCode)
				continue
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("%s hdr=%q: content type = %q, want text/html (degraded)", tc.path, val, ct)
			}
			if strings.Contains(body, `"version":`) || strings.Contains(body, `"routeId"`) {
				t.Errorf("%s hdr=%q: unsupported version still returned an envelope", tc.path, val)
			}
			if !strings.Contains(body, tc.marker) {
				t.Errorf("%s hdr=%q: degraded response lost its %s marker", tc.path, val, tc.marker)
			}
		}
	}
}

// TestF04EnvelopeWithoutJSONAccept: the server keys the envelope on the
// navigation header, not on Accept negotiation — a client sending the
// header with a broad Accept still gets the envelope. Documents the gate
// precisely: only the header version decides (blueprint 5.2 step 5).
func TestF04EnvelopeWithoutJSONAccept(t *testing.T) {
	app := newApp(nil)
	req := httptest.NewRequest("GET", "/about", nil)
	req.Header.Set(httpserver.NavigationHeader, "1")
	req.Header.Set("Accept", "*/*")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q, want application/json despite Accept */*", ct)
	}
	if !strings.Contains(string(b), `"mode":"ssr"`) {
		t.Errorf("expected SSR envelope with Accept */*, got: %s", clip(string(b)))
	}
}

// TestF04NonGETStaysMethodRestricted: the envelope contract covers GET
// only; other methods keep their 405 even when carrying the header.
func TestF04NonGETStaysMethodRestricted(t *testing.T) {
	app := newApp(nil)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/about", nil)
		req.Header.Set(httpserver.NavigationHeader, "1")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 405 {
			t.Errorf("%s /about + header: status = %d, want 405", method, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(b), `"version":1`) {
			t.Errorf("%s /about + header: envelope served for non-GET", method)
		}
	}
}

// TestF04UnknownRoute404BothWays: unknown paths serve the centralized 404
// document regardless of the header — the envelope contract belongs to
// registered page routes only.
func TestF04UnknownRoute404BothWays(t *testing.T) {
	app := newApp(nil)
	for _, val := range []string{"", "1", "2"} {
		resp, body := doGet(t, app, "/definitely-not-a-page", val)
		if resp.StatusCode != 404 {
			t.Errorf("hdr=%q: status = %d, want 404", val, resp.StatusCode)
		}
		if !strings.Contains(body, `data-go-error="404"`) {
			t.Errorf("hdr=%q: missing centralized 404 page", val)
		}
		if strings.Contains(body, `"version":1`) {
			t.Errorf("hdr=%q: unknown route leaked an envelope", val)
		}
	}
}
