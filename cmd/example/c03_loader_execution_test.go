package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"

	"go-meta/app/loaders"
	"go-meta/loader"
	"go-meta/routecontext"
)

// getBody issues a request against app and returns the status and body.
func getBody(t *testing.T, app *fiber.App, name string, url string, nav bool) (int, string) {
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
	return res.StatusCode, string(body)
}

// TestC03SSRExecutesRegisteredLoaderEndToEnd drives the real app (real
// registry from loaders.Bindings, real generated routes) and proves the
// registered loader runs during SSR dispatch and its typed result reaches the
// templ page, for both full-document and navigation-envelope responses.
func TestC03SSRExecutesRegisteredLoaderEndToEnd(t *testing.T) {
	registry, err := newLoaderRegistry(loaders.Bindings())
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// Full document: the loader's typed result is rendered by the page.
	code, body := getBody(t, app, "posts full", "/posts/42", false)
	if code != 200 {
		t.Fatalf("full status=%d body=%q", code, body)
	}
	for _, want := range []string{`<p data-post-id>42</p>`, "Example Post", "Loaded on the server", `data-go-layout="app/layout.templ"`} {
		if !strings.Contains(body, want) {
			t.Errorf("full doc missing %q in: %s", want, body)
		}
	}
	if strings.Contains(body, "No loader data for this route.") {
		t.Errorf("full doc shows the no-data fallback even though a loader ran: %s", body)
	}

	// Navigation envelope: version 1, correct route ID, and the rendered HTML
	// (layout + page) contains the loaded data. No new data field is added.
	code, body = getBody(t, app, "posts envelope", "/posts/42", true)
	if code != 200 {
		t.Fatalf("envelope status=%d body=%q", code, body)
	}
	var env struct {
		Version int    `json:"version"`
		RouteID string `json:"routeId"`
		HTML    string `json:"html"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope not JSON: %v in %q", err, body)
	}
	if env.Version != 1 || env.RouteID != "posts/[postId]/page.templ" {
		t.Fatalf("envelope version=%d routeId=%q", env.Version, env.RouteID)
	}
	if !strings.Contains(env.HTML, "Example Post") || !strings.Contains(env.HTML, "Loaded on the server") {
		t.Errorf("envelope HTML missing loaded data: %s", env.HTML)
	}

	// Same declared route, different concrete URL: the loader re-runs with the
	// new parameter and the page reflects it.
	code, body = getBody(t, app, "posts 99", "/posts/99", false)
	if code != 200 || !strings.Contains(body, `<p data-post-id>99</p>`) {
		t.Fatalf("posts/99 status=%d body=%q", code, body)
	}

	// Loader failure (unparseable postId) stops the render: a generic 500, no
	// page content, and no raw internal error leaked.
	code, body = getBody(t, app, "posts invalid", "/posts/abc", false)
	if code != 500 {
		t.Fatalf("invalid postId status=%d, want 500 (loader failure must stop render)", code)
	}
	if strings.Contains(body, "Example Post") || strings.Contains(body, "Loaded on the server") {
		t.Errorf("failed loader still rendered page content: %s", body)
	}
	if strings.Contains(body, "invalid value") || strings.Contains(body, "strconv") {
		t.Errorf("raw loader error leaked to client: %s", body)
	}

	// A route with no registered loader renders normally.
	code, body = getBody(t, app, "about no-loader", "/about", false)
	if code != 200 {
		t.Fatalf("about status=%d", code)
	}
}

// TestC03LoaderExecutionCountAndIsolation uses a custom registry with counting
// loaders to prove: SSR loaders run exactly once per request, CSR loaders are
// never run, the loader receives the correct route snapshot (params + query),
// and concurrent requests are isolated.
func TestC03LoaderExecutionCountAndIsolation(t *testing.T) {
	var ssrCalls, csrCalls atomic.Int32
	var captured routecontext.Route
	ssrLoad := func(_ context.Context, r routecontext.Route) (int, error) {
		ssrCalls.Add(1)
		captured = r
		return 1, nil
	}
	csrLoad := func(context.Context, routecontext.Route) (int, error) {
		csrCalls.Add(1)
		return 1, nil
	}
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("about/page.templ", ssrLoad),
		loader.Bind("products/[id]/page.svelte", csrLoad),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// SSR route: loader runs once, with the correct snapshot.
	code, _ := getBody(t, app, "about", "/about?tag=x&tag=y", false)
	if code != 200 {
		t.Fatalf("about status=%d", code)
	}
	if ssrCalls.Load() != 1 {
		t.Fatalf("SSR loader ran %d times, want 1", ssrCalls.Load())
	}
	if captured.RouteID != "about/page.templ" {
		t.Fatalf("loader route ID=%q", captured.RouteID)
	}
	if tags := captured.Query.Values("tag"); len(tags) != 2 || tags[0] != "x" || tags[1] != "y" {
		t.Fatalf("loader query=%v", tags)
	}

	// A second request runs the loader again (once per request).
	getBody(t, app, "about again", "/about", false)
	if ssrCalls.Load() != 2 {
		t.Fatalf("SSR loader ran %d times after 2 requests, want 2", ssrCalls.Load())
	}

	// CSR route: the registered loader must NOT run.
	code, _ = getBody(t, app, "csr", "/products/42", false)
	if code != 200 {
		t.Fatalf("csr status=%d", code)
	}
	if csrCalls.Load() != 0 {
		t.Fatalf("CSR loader ran %d times, want 0 (C03 must not execute CSR loaders)", csrCalls.Load())
	}

	// Unmatched and invalid requests run no loader.
	if code, _ := getBody(t, app, "404", "/definitely-not-a-route", false); code != 404 {
		t.Fatalf("404 status=%d", code)
	}
	if code, _ := getBody(t, app, "invalid", "/%2e%2e/secret", false); code != 400 {
		t.Fatalf("invalid path status=%d, want 400", code)
	}
	if ssrCalls.Load() != 2 {
		t.Fatalf("loader ran on unmatched/invalid requests: %d", ssrCalls.Load())
	}

	// Concurrent requests: each runs the loader exactly once, isolated.
	const n = 32
	before := ssrCalls.Load()
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if code, _ := getBody(t, app, "concurrent", "/about", false); code != 200 {
				t.Errorf("concurrent status=%d", code)
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	if got := ssrCalls.Load(); got != before+n {
		t.Fatalf("concurrent: loader ran %d times, want %d", got-before, n)
	}
}

// TestC03LoaderFailureStopsRenderAndHidesError binds a failing loader to an
// SSR route and proves the page does not render and the client sees only a
// generic error, never the loader's message.
func TestC03LoaderFailureStopsRenderAndHidesError(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("about/page.templ", func(context.Context, routecontext.Route) (int, error) {
			return 0, context.DeadlineExceeded
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	code, body := getBody(t, app, "failing", "/about", false)
	if code != 500 {
		t.Fatalf("status=%d, want 500", code)
	}
	if strings.Contains(body, "About") && strings.Contains(body, "data-go-page") {
		t.Errorf("page rendered despite loader failure: %s", body)
	}
	for _, secret := range []string{"DeadlineExceeded", "deadline", "panic"} {
		if strings.Contains(body, secret) {
			t.Errorf("internal error %q leaked: %s", secret, body)
		}
	}
}
