package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go-meta/loader"
	"go-meta/routecontext"
)

func TestC02RegistryUsesGeneratedHTTPIdentities(t *testing.T) {
	// C03 executes SSR loaders and C04 executes CSR loaders during dispatch, so
	// the counters are tracked separately.
	var ssrCalls, csrCalls atomic.Int32
	ssr := "users/[userId]/posts/[postId]/page.templ"
	csr := "products/[id]/page.svelte"
	catch := "docs/[...slug]/page.templ"
	ssrLoad := func(context.Context, routecontext.Route) (int, error) { ssrCalls.Add(1); return 1, nil }
	csrLoad := func(context.Context, routecontext.Route) (int, error) { csrCalls.Add(1); return 1, nil }
	registry, err := newLoaderRegistry([]loader.Binding{loader.Bind(ssr, ssrLoad), loader.Bind(csr, csrLoad), loader.Bind(catch, ssrLoad)})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	if stored, ok := fiber.GetState[*loader.Registry](app.State(), loaderRegistryKey); !ok || stored != registry {
		t.Fatal("registry not retained by app")
	}
	for _, tc := range []struct {
		url, id string
		has     bool
	}{
		{"/users/42/posts/7", ssr, true}, {"/users/99/posts/8", ssr, true},
		{"/products/42", csr, true}, {"/products/99", csr, true},
		{"/docs/a/b", catch, true}, {"/docs/c/d", catch, true}, {"/about", "about/page.templ", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			direct, err := app.Test(httptest.NewRequest("GET", tc.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			direct.Body.Close()
			if direct.StatusCode != 200 || !strings.Contains(direct.Header.Get("Content-Type"), "text/html") {
				t.Fatalf("direct status=%d content-type=%q", direct.StatusCode, direct.Header.Get("Content-Type"))
			}
			req := httptest.NewRequest("GET", tc.url, nil)
			req.Header.Set("X-Go-Web-Navigation", "1")
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("status=%d", res.StatusCode)
			}
			var envelope struct {
				Version int    `json:"version"`
				RouteID string `json:"routeId"`
			}
			if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			_, ok := registry.Lookup(envelope.RouteID)
			if envelope.Version != 1 || envelope.RouteID != tc.id || ok != tc.has {
				t.Fatalf("envelope=%+v registered=%v", envelope, ok)
			}
		})
	}
	// C03/C04: each route below is hit twice (direct + navigation). SSR routes
	// (users ×2, docs catch-all ×2) → 8 runs; CSR routes (products ×2) → 4 runs.
	if ssrCalls.Load() != 8 {
		t.Fatalf("SSR loader ran %d times, want 8", ssrCalls.Load())
	}
	if csrCalls.Load() != 4 {
		t.Fatalf("CSR loader ran %d times, want 4 (C04 executes CSR loaders)", csrCalls.Load())
	}
}
