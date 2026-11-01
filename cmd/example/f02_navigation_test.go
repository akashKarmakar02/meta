package main

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-meta/internal/httpserver"
)

// navGet issues a GET with the navigation header, returning status, body, and content type.
func navGet(t *testing.T, app *fiber.App, path string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set(httpserver.NavigationHeader, "1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Content-Type")
}

// TestNavigationSSRHeaderAbout verifies F02: GET /about with the navigation
// header returns a NavigationResponse JSON envelope with SSR HTML and metadata.
func TestNavigationSSRHeaderAbout(t *testing.T) {
	app := newApp(nil)

	status, body, ctype := navGet(t, app, "/about")
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.HasPrefix(ctype, "application/json") {
		t.Fatalf("content type = %q, want application/json", ctype)
	}

	var nav httpserver.NavigationResponse
	if err := json.Unmarshal([]byte(body), &nav); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if nav.Version != httpserver.NavigationVersion {
		t.Errorf("version = %d, want %d", nav.Version, httpserver.NavigationVersion)
	}
	if nav.Mode != "ssr" {
		t.Errorf("mode = %q, want ssr", nav.Mode)
	}
	if nav.URL != "/about" {
		t.Errorf("url = %q, want /about", nav.URL)
	}
	if nav.RouteID != "about/page.templ" {
		t.Errorf("routeId = %q, want about/page.templ", nav.RouteID)
	}
	if nav.Title != "About" {
		t.Errorf("title = %q, want About", nav.Title)
	}
	if len(nav.Layouts) == 0 || nav.Layouts[0] != "app/layout.templ" {
		t.Errorf("layouts = %v, want first layout app/layout.templ", nav.Layouts)
	}
	if !strings.Contains(nav.HTML, "<h1>About</h1>") {
		t.Errorf("html missing About heading: %q", nav.HTML)
	}
	if strings.Contains(nav.HTML, "<html") || strings.Contains(nav.HTML, "<!DOCTYPE") {
		t.Errorf("html must be the bare page outlet, not a full document: %q", nav.HTML)
	}
	if nav.ClientEntry != nil {
		t.Errorf("clientEntry = %v, want nil for SSR", *nav.ClientEntry)
	}
}

// TestNavigationNoStoreHeaderPresent confirms the no-store cache header on nav responses.
func TestNavigationNoStoreHeaderPresent(t *testing.T) {
	app := newApp(nil)

	req := httptest.NewRequest("GET", "/about", nil)
	req.Header.Set(httpserver.NavigationHeader, "1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// TestNavigationNoHeaderFullDocument verifies the header is what triggers the
// envelope: without it, /about serves the usual full SSR document.
func TestNavigationNoHeaderFullDocument(t *testing.T) {
	app := newApp(nil)

	resp, err := app.Test(httptest.NewRequest("GET", "/about", nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type = %q, want text/html", ct)
	}
	if !strings.Contains(strings.ToLower(body), "<!doctype") {
		t.Errorf("expected full document without header, got: %q", clip(body))
	}
	if strings.Contains(body, `"mode":"ssr"`) {
		t.Errorf("navigation envelope leaked into non-header request")
	}
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
