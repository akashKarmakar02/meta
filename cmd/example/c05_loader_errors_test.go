package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
)

// doRequest issues a request against app and returns the status, body, and
// response headers.
func doRequest(t *testing.T, app *fiber.App, name, url string, nav bool) (int, string, http.Header) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	if nav {
		req.Header.Set("X-Go-Web-Navigation", "1")
	}
	res, err := app.Test(req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body), res.Header
}

// TestC05LoaderErrorsMapToStatuses proves the C01 error sentinels (and wrapped
// variants) map to their correct HTTP statuses, that cancellation and deadline
// are handled distinctly, and that internal errors never leak to the client.
func TestC05LoaderErrorsMapToStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", loader.ErrNotFound, 404},
		{"unauthorized", loader.ErrUnauthorized, 401},
		{"forbidden", loader.ErrForbidden, 403},
		{"invalid input", loader.ErrInvalidInput, 400},
		{"wrapped not found", fmt.Errorf("lookup failed: %w", loader.ErrNotFound), 404},
		{"joined forbidden", errors.Join(errors.New("audit note"), loader.ErrForbidden), 403},
		{"deadline exceeded", context.DeadlineExceeded, 504},
		{"canceled", context.Canceled, 499},
		{"generic internal", errors.New("database down: secret detail"), 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := newLoaderRegistry([]loader.Binding{
				loader.Bind("about/page.templ", func(context.Context, routecontext.Route) (int, error) {
					return 0, tc.err
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			app := newConfiguredApp(nil, false, registry)
			code, body, _ := doRequest(t, app, tc.name, "/about", false)
			if code != tc.want {
				t.Fatalf("status=%d, want %d", code, tc.want)
			}
			// The page must not render when the loader fails.
			if strings.Contains(body, `data-go-page`) {
				t.Errorf("page rendered despite loader failure: %s", body)
			}
			// Internal errors must not leak their message.
			if strings.Contains(body, "secret detail") {
				t.Errorf("internal error leaked to client: %s", body)
			}
		})
	}
}

// TestC05SSRRedirect proves a loader redirect produces a 302 with a
// root-relative Location on a direct SSR request, and that soft navigation
// receives the same 302 (the client's fetch follows it; the target then
// returns the envelope for the final URL).
func TestC05SSRRedirect(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("about/page.templ", func(context.Context, routecontext.Route) (int, error) {
			return 0, loader.Redirect("/docs")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// Direct request: 302 with a root-relative Location, empty body.
	code, body, hdr := doRequest(t, app, "ssr redirect", "/about", false)
	if code != 302 {
		t.Fatalf("status=%d, want 302", code)
	}
	if got := hdr.Get("Location"); got != "/docs" {
		t.Fatalf("Location=%q, want /docs", got)
	}
	if body != "" {
		t.Errorf("redirect body should be empty: %q", body)
	}

	// Soft navigation: the first hop is still a 302; the browser's fetch
	// follows it (same-origin, navigation header preserved) and the target
	// returns the envelope for the final URL.
	code, _, hdr = doRequest(t, app, "nav redirect", "/about", true)
	if code != 302 || hdr.Get("Location") != "/docs" {
		t.Fatalf("nav redirect status=%d Location=%q, want 302 /docs", code, hdr.Get("Location"))
	}
	code, body, _ = doRequest(t, app, "nav target", "/docs", true)
	if code != 200 {
		t.Fatalf("nav target status=%d", code)
	}
	var env struct {
		Version int    `json:"version"`
		RouteID string `json:"routeId"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("nav target not an envelope: %v in %q", err, body)
	}
	if env.Version != 1 || env.RouteID != "docs/page.templ" {
		t.Fatalf("nav target envelope version=%d routeId=%q", env.Version, env.RouteID)
	}
}

// TestC05CSRRedirect proves a loader redirect works on a direct CSR request
// (302 with root-relative Location) — the browser follows it to the target.
func TestC05CSRRedirect(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("products/[id]/page.svelte", func(context.Context, routecontext.Route) (int, error) {
			return 0, loader.Redirect("/products/new")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	code, _, hdr := doRequest(t, app, "csr redirect", "/products/42", false)
	if code != 302 {
		t.Fatalf("status=%d, want 302", code)
	}
	if got := hdr.Get("Location"); got != "/products/new" {
		t.Fatalf("Location=%q, want /products/new", got)
	}
}

// TestC05InvalidRedirectIsInternalError proves a redirect to a non-root-relative
// target (absolute URL, protocol-relative, or other scheme) is rejected and
// surfaces as a 500 — never as an open redirect.
func TestC05InvalidRedirectIsInternalError(t *testing.T) {
	for _, target := range []string{
		"https://evil.example.com/steal",
		"http://evil.example.com/steal",
		"//evil.example.com/steal",
		"javascript:alert(1)",
	} {
		registry, err := newLoaderRegistry([]loader.Binding{
			loader.Bind("about/page.templ", func(context.Context, routecontext.Route) (int, error) {
				return 0, loader.Redirect(target)
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		app := newConfiguredApp(nil, false, registry)
		code, body, hdr := doRequest(t, app, "bad redirect", "/about", false)
		if code != 500 {
			t.Fatalf("target %q: status=%d, want 500", target, code)
		}
		if hdr.Get("Location") != "" {
			t.Errorf("target %q: Location set on a 500: %q", target, hdr.Get("Location"))
		}
		if strings.Contains(body, "evil.example.com") || strings.Contains(body, "javascript:") {
			t.Errorf("target %q: redirect target leaked: %s", target, body)
		}
	}
}

// TestC05ErrorsPreventCSRDataPublication proves a failing CSR loader stops the
// shell/envelope from being served with data: the client receives the error
// status, and no loader data script or envelope is published.
func TestC05ErrorsPreventCSRDataPublication(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("products/[id]/page.svelte", func(context.Context, routecontext.Route) (int, error) {
			return 0, loader.ErrForbidden
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// Direct CSR request: 403, no data script.
	code, body, _ := doRequest(t, app, "csr forbidden", "/products/42", false)
	if code != 403 {
		t.Fatalf("status=%d, want 403", code)
	}
	if strings.Contains(body, `data-go-csr-data`) {
		t.Errorf("loader data published despite failure: %s", body)
	}

	// Navigation request: 403, no envelope.
	code, body, _ = doRequest(t, app, "csr forbidden nav", "/products/42", true)
	if code != 403 {
		t.Fatalf("nav status=%d, want 403", code)
	}
	if strings.Contains(body, `"version"`) || strings.Contains(body, `data-go-csr-data`) {
		t.Errorf("envelope or data published despite failure: %s", body)
	}
}
