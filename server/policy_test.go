package server

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/action"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/requeststate"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routing"
	"github.com/gofiber/fiber/v3"
)

func TestPolicyPrecedenceAndRequestContext(t *testing.T) {
	key := requeststate.NewKey[string]()
	var calls atomic.Int32
	auth := func(c fiber.Ctx) error { requeststate.Set(c, key, c.Get("X-User")); return c.Next() }
	access := func(c fiber.Ctx) error {
		if c.Get("X-Access") != "yes" {
			return c.SendStatus(403)
		}
		return c.Next()
	}
	admin := func(c fiber.Ctx) error {
		if c.Get("X-Admin") != "yes" {
			return c.SendStatus(403)
		}
		return c.Next()
	}
	endpoint := func(c fiber.Ctx) error {
		calls.Add(1)
		user, _ := requeststate.Get(c.Context(), key)
		return c.SendString(user + ":" + c.Params("id"))
	}
	app, err := New(Config{
		Middleware: []fiber.Handler{func(c fiber.Ctx) error { c.Set("X-Hook", "yes"); return c.Next() }},
		Policies:   []Policy{Protect("/dashboard/*", auth, access), Protect("/dashboard/billing", auth, admin)},
		APIRoutes: []APIRoute{
			{Path: "/dashboard/:id", SourceFile: "dashboard/[id]/route.go", Handlers: map[string]fiber.Handler{"GET": endpoint}},
			{Path: "/dashboard/billing", SourceFile: "dashboard/billing/route.go", Handlers: map[string]fiber.Handler{"GET": endpoint}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, access, admin string
		status              int
		body                string
	}{
		{"/dashboard/billing", "yes", "", 403, ""},
		{"/dashboard/billing/", "", "yes", 200, "alice:"},
		{"/dashboard/settings", "", "yes", 403, ""},
		{"/dashboard/settings", "yes", "", 200, "alice:settings"},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("X-User", "alice")
		req.Header.Set("X-Access", tc.access)
		req.Header.Set("X-Admin", tc.admin)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.status || (tc.body != "" && string(body) != tc.body) || resp.Header.Get("X-Hook") != "yes" || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
			t.Fatalf("%s: status=%d body=%q headers=%v", tc.path, resp.StatusCode, body, resp.Header)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("endpoint executed %d times", calls.Load())
	}
}

func TestPolicyProtectsPageAndLoader(t *testing.T) {
	key := requeststate.NewKey[string]()
	var calls atomic.Int32
	resolver := func(r routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, "protected")
			return err
		}), nil
	}
	app, err := New(Config{
		Routes:  []routing.Route{{ID: "dashboard/page.templ", SourceFile: "dashboard/page.templ", Path: "/dashboard", Mode: routing.ModeSSR}},
		SSRPage: resolver, BarePage: resolver,
		VisualLayout: func(_ routing.Route, component templ.Component) (templ.Component, error) { return component, nil },
		Policies: []Policy{Protect("/dashboard/*", func(c fiber.Ctx) error {
			if c.Get("X-User") == "" {
				return c.SendStatus(401)
			}
			requeststate.Set(c, key, c.Get("X-User"))
			return c.Next()
		})},
		Loaders: []loader.Binding{loader.Bind("dashboard/page.templ", func(ctx context.Context, _ routecontext.Route) (string, error) {
			calls.Add(1)
			user, _ := requeststate.Get(ctx, key)
			if user != "alice" {
				t.Errorf("loader user = %q", user)
			}
			return user, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, navigation := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/dashboard", nil)
		if navigation {
			req.Header.Set("X-Go-Web-Navigation", "1")
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("navigation=%v: %d", navigation, resp.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("denied request ran loader")
	}
	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.Header.Set("X-User", "alice")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d", resp.StatusCode, calls.Load())
	}
}

func TestActionPolicyDoesNotBypassOrigin(t *testing.T) {
	key := requeststate.NewKey[string]()
	var calls atomic.Int32
	app, err := New(Config{TrustedOrigin: "https://example.com", Policies: []Policy{Protect("/_meta/actions/save", func(c fiber.Ctx) error { requeststate.Set(c, key, "user"); return c.Next() })}, Actions: []action.Binding{action.Bind("save", func(ctx context.Context, _ routecontext.Route, in struct{}) (string, error) {
		calls.Add(1)
		v, _ := requeststate.Get(ctx, key)
		return v, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		origin string
		status int
	}{{"https://evil.example", 403}, {"https://example.com", 200}} {
		req := httptest.NewRequest("POST", "/_meta/actions/save", strings.NewReader("{}"))
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("origin %s: %d", tc.origin, resp.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("action calls=%d", calls.Load())
	}
}

func TestRejectInvalidPolicies(t *testing.T) {
	h := func(c fiber.Ctx) error { return c.Next() }
	for _, policies := range [][]Policy{{Protect("dashboard/*", h)}, {Protect("/x/:id", h)}, {Protect("/x//y", h)}, {Protect("/x/*", h), Protect("/x/*", h)}, {Protect("/x", nil)}} {
		if _, err := New(Config{Policies: policies}); err == nil {
			t.Fatalf("accepted %v", policies)
		}
	}
}
