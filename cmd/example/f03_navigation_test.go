package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-meta/internal/httpserver"
)

// F03: CSR routes return navigation envelopes on the navigation request:
// empty html, clientEntry keyed to the generated import registry, and real
// discovered layout ancestry. Requests without the header (or with an
// unsupported version) keep receiving ordinary boot shells.

// httptestReq builds a GET request, setting the navigation header when the
// value is non-empty.
func httptestReq(method, path, headerValue string) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	if headerValue != "" {
		req.Header.Set(httpserver.NavigationHeader, headerValue)
	}
	return req
}

func readAll(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// navGetRaw issues a GET with an explicit header value, returning status and body.
func navGetRaw(t *testing.T, app *fiber.App, path, headerValue string) (int, string) {
	t.Helper()
	req := httptestReq("GET", path, headerValue)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, readAll(resp)
}

func TestF03DashboardNavigationEnvelope(t *testing.T) {
	app := newApp(nil)

	status, body := navGetRaw(t, app, "/dashboard", "1")
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	var env httpserver.NavigationResponse
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Version != 1 {
		t.Errorf("version = %d, want 1", env.Version)
	}
	if env.URL != "/dashboard" {
		t.Errorf("url = %q, want /dashboard", env.URL)
	}
	if env.RouteID != "dashboard/page.svelte" {
		t.Errorf("routeId = %q, want dashboard/page.svelte", env.RouteID)
	}
	if env.Mode != "csr" {
		t.Errorf("mode = %q, want csr", env.Mode)
	}
	if env.Title != "Dashboard" {
		t.Errorf("title = %q, want Dashboard", env.Title)
	}
	if env.HTML != "" {
		t.Errorf("html = %q, want exactly \"\" for CSR envelopes", env.HTML)
	}
	if env.ClientEntry == nil || *env.ClientEntry != "dashboard/page.svelte" {
		t.Errorf("clientEntry = %v, want dashboard/page.svelte (registry key)", env.ClientEntry)
	}
	if len(env.Layouts) != 1 || env.Layouts[0] != "app/layout.templ" {
		t.Errorf("layouts = %v, want [app/layout.templ] (actual ancestry)", env.Layouts)
	}
}

func TestF03SettingsNavigationEnvelope(t *testing.T) {
	app := newApp(nil)

	status, body := navGetRaw(t, app, "/dashboard/settings", "1")
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	var env httpserver.NavigationResponse
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	if env.RouteID != "dashboard/settings/page.svelte" {
		t.Errorf("routeId = %q, want dashboard/settings/page.svelte", env.RouteID)
	}
	if env.ClientEntry == nil || *env.ClientEntry != "dashboard/settings/page.svelte" {
		t.Errorf("clientEntry = %v, want dashboard/settings/page.svelte", env.ClientEntry)
	}
	if env.Title != "Settings" {
		t.Errorf("title = %q, want Settings", env.Title)
	}
	if env.HTML != "" {
		t.Errorf("html = %q, want exactly \"\" for CSR envelopes", env.HTML)
	}
}

func TestF03ContentTypeAndCacheControl(t *testing.T) {
	app := newApp(nil)

	req := httptestReq("GET", "/dashboard", "1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control = %q, want no-store", cc)
	}
}

func TestF03NoHeaderStillServesShell(t *testing.T) {
	app := newApp(nil)

	req := httptestReq("GET", "/dashboard", "")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(resp)
	if !strings.Contains(body, "data-go-page-root") {
		t.Errorf("ordinary CSR request lost boot shell: %q", body)
	}
}

func TestF03UnsupportedHeaderVersionServesShell(t *testing.T) {
	// Blueprint 5.2: unknown version degrades to a normal full-page response
	// rather than an envelope the browser cannot act on.
	app := newApp(nil)

	status, body := navGetRaw(t, app, "/dashboard", "99")
	if status != 200 {
		t.Fatalf("status = %d, want 200 (degraded shell), got %d", status, status)
	}
	if strings.Contains(body, `"mode":"csr"`) {
		t.Errorf("unsupported version returned an envelope: %q", body)
	}
	if !strings.Contains(body, "data-go-page-root") {
		t.Errorf("unsupported version did not fall back to shell: %q", body)
	}
}

func TestF03SSRNavigationUnchanged(t *testing.T) {
	app := newApp(nil)

	status, body := navGetRaw(t, app, "/about", "1")
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	var env httpserver.NavigationResponse
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	if env.Mode != "ssr" {
		t.Errorf("ssr route mode = %q, want ssr", env.Mode)
	}
	if env.HTML == "" {
		t.Error("ssr envelope lost bare page html")
	}
	if env.ClientEntry != nil {
		t.Errorf("ssr clientEntry = %v, want null", env.ClientEntry)
	}
}
