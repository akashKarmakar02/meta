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
	var calls atomic.Int32
	ssr := "users/[userId]/posts/[postId]/page.templ"
	csr := "products/[id]/page.svelte"
	catch := "docs/[...slug]/page.templ"
	load := func(context.Context, routecontext.Route) (int, error) { calls.Add(1); return 1, nil }
	registry, err := newLoaderRegistry([]loader.Binding{loader.Bind(ssr, load), loader.Bind(csr, load), loader.Bind(catch, load)})
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
	if calls.Load() != 0 {
		t.Fatalf("HTTP invoked loader %d times", calls.Load())
	}
}
