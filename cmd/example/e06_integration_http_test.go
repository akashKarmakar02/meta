package main

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apploaders "go-meta/app/loaders"
	"go-meta/cache"
	"go-meta/loader"
	"go-meta/routecontext"

	"github.com/gofiber/fiber/v3"
)

// tenantMiddleware publishes a request-scoped principal value — exactly how
// real auth middleware would install a tenant before loaders run — that a
// loader's scope callback folds into its cache scope.
type e06TenantKey struct{}

func e06TenantMiddleware(c fiber.Ctx) error {
	if v := c.Get("X-Test-Tenant"); v != "" {
		c.SetContext(context.WithValue(c.Context(), e06TenantKey{}, v))
	}
	return c.Next()
}

// getWithTenant issues a GET carrying the request-scoped tenant header.
func getWithTenant(t *testing.T, app *fiber.App, name, url, tenant string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	if tenant != "" {
		req.Header.Set("X-Test-Tenant", tenant)
	}
	res, err := app.Test(req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// TestE06HTTPTenantScopesNeverShareCacheEntries (§14): two properly
// distinct tenant scopes over the SAME route, params and query — the only
// differing input is the request-scoped tenant value. Distinct scopes
// must produce distinct cache entries: neither tenant may see the
// other's data, a per-tenant miss must execute that tenant's own loader,
// a per-tenant hit must reuse only that tenant's entry, and a public
// (untenanted) request must not see either tenant's entry.
//
// This test intentionally omits Flights: the loader reads a
// request-specific context value, which shared flight work does not
// receive (see TestE06HTTPSingleflightDropsRequestValues).
func TestE06HTTPTenantScopesNeverShareCacheEntries(t *testing.T) {
	store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inv, err := loader.NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	load := func(ctx context.Context, r routecontext.Route) (apploaders.CacheDemoData, error) {
		calls.Add(1)
		tenant, _ := ctx.Value(e06TenantKey{}).(string)
		if tenant == "" {
			return apploaders.CacheDemoData{ID: "42", Refreshed: "public-data"}, nil
		}
		return apploaders.CacheDemoData{
			ID:        "42",
			Refreshed: "tenant-" + tenant + "-data",
		}, nil
	}
	opts := loader.CacheOptions[apploaders.CacheDemoData]{
		Store: store, Invalidator: inv,
		Policy: cache.Policy{TTL: time.Minute},
		Codec:  loader.JSONCodec[apploaders.CacheDemoData](),
		Scope: func(ctx context.Context, _ routecontext.Route) (string, bool, error) {
			tenant, _ := ctx.Value(e06TenantKey{}).(string)
			if tenant == "" {
				return "public", true, nil
			}
			return "tenant:" + tenant, true, nil
		},
	}
	reg, err := newLoaderRegistry([]loader.Binding{
		loader.BindCached("cache-demo/post/[id]/page.templ", load, opts),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, reg, e06TenantMiddleware)

	// First tenant-a request: miss, own loader runs.
	code, body := getWithTenant(t, app, "a-1", "/cache-demo/post/42", "tenant-a")
	if code != 200 || !strings.Contains(body, "tenant-a-data") {
		t.Fatalf("tenant A first response: %d contains=%v", code,
			strings.Contains(body, "tenant-a-data"))
	}
	// Second tenant-a request: HIT on its own entry, no new execution.
	code, body = getWithTenant(t, app, "a-2", "/cache-demo/post/42", "tenant-a")
	if code != 200 || !strings.Contains(body, "tenant-a-data") {
		t.Fatalf("tenant A cached response: %d contains=%v", code,
			strings.Contains(body, "tenant-a-data"))
	}
	// Tenant B over the same URL: must miss and run its OWN loader —
	// never tenant A's entry.
	code, body = getWithTenant(t, app, "b-1", "/cache-demo/post/42", "tenant-b")
	if code != 200 || !strings.Contains(body, "tenant-b-data") {
		t.Fatalf("tenant B response: %d contains=%v", code,
			strings.Contains(body, "tenant-b-data"))
	}
	// Public request: distinct scope again, must see public data, not
	// either tenant's.
	code, body = getWithTenant(t, app, "public", "/cache-demo/post/42", "")
	if code != 200 || !strings.Contains(body, "public-data") {
		t.Fatalf("public response saw tenant data: %d contains-a=%v",
			code, strings.Contains(body, "tenant-a-data"))
	}
	// Exactly three executions: one per scope. Everything else hit.
	if n := calls.Load(); n != 3 {
		t.Fatalf("expected 3 loader executions (one per scope), got %d", n)
	}
}

// TestE06HTTPSingleflightDropsRequestValues (§26): the E05 documented
// contract — shared flight work receives route metadata but NOT
// request-specific context values. Over real HTTP this means a loader
// whose output depends on a request context value MUST opt out of
// singleflight (or of caching) in its scope callback; if it does not,
// the flight leader computes without those values and every joiner
// receives that leader result. This test pins that contract so a future
// regression toward silent context copying (which would leak one
// request's principal into another request's shared work) is caught.
func TestE06HTTPSingleflightDropsRequestValues(t *testing.T) {
	store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	inv, err := loader.NewInvalidator(store)
	if err != nil {
		t.Fatal(err)
	}
	group, err := loader.NewFlightGroup(inv, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var sawTenant atomic.Value // string
	load := func(ctx context.Context, r routecontext.Route) (apploaders.CacheDemoData, error) {
		v, _ := ctx.Value(e06TenantKey{}).(string)
		sawTenant.Store(v)
		return apploaders.CacheDemoData{ID: "42", Refreshed: "computed"}, nil
	}
	opts := loader.CacheOptions[apploaders.CacheDemoData]{
		Store: store, Invalidator: inv, Flights: group,
		Policy: cache.Policy{TTL: time.Minute},
		Codec:  loader.JSONCodec[apploaders.CacheDemoData](),
		Scope: func(ctx context.Context, _ routecontext.Route) (string, bool, error) {
			// The scope callback evaluates per-request WITH the request
			// context — tenant isolation of keys is unaffected.
			tenant, _ := ctx.Value(e06TenantKey{}).(string)
			return "tenant:" + tenant, true, nil
		},
	}
	reg, err := newLoaderRegistry([]loader.Binding{
		loader.BindCached("cache-demo/post/[id]/page.templ", load, opts),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, reg, e06TenantMiddleware)

	code, body := getWithTenant(t, app, "flight", "/cache-demo/post/42", "tenant-a")
	if code != 200 || !strings.Contains(body, "computed") {
		t.Fatalf("flight response: %d", code)
	}
	// The shared fill ran in the flight context: the request's tenant
	// value was NOT visible to the loader. This is the documented
	// limitation; such loaders must bypass singleflight via their scope
	// callback.
	if v, _ := sawTenant.Load().(string); v != "" {
		t.Fatalf("request context value leaked into flight-owned context: %q", v)
	}
}
