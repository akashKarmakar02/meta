package httpserver

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go-meta/internal/routing"
)

// B03: a catch-all route captures one or more segments, decoded per segment,
// stored under the route's declared name. The value is slash-joined.
func TestRouteParamsCatchAll(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/docs/+", CatchAll: "slug"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error { return c.JSON(RouteParams(c)) }))
	for _, tc := range []struct {
		name, url string
		status    int
		want      map[string]string
	}{
		{"single segment", "/docs/a", 200, map[string]string{"slug": "a"}},
		{"two segments", "/docs/a/b", 200, map[string]string{"slug": "a/b"}},
		{"three segments", "/docs/a/b/c", 200, map[string]string{"slug": "a/b/c"}},
		{"decoded segments", "/docs/getting%20started/installation", 200, map[string]string{"slug": "getting started/installation"}},
		{"unicode segment", "/docs/a/%E6%9D%B1%E4%BA%AC", 200, map[string]string{"slug": "a/東京"}},
		{"zero segments", "/docs", 404, nil},
		{"encoded slash", "/docs/a%2Fb", 400, nil},
		{"encoded backslash", "/docs/a%5Cb", 400, nil},
		{"encoded dot", "/docs/%2e", 400, nil},
		{"encoded dotdot", "/docs/a%2F..%2Fb", 400, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := app.Test(httptest.NewRequest("GET", tc.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", res.StatusCode, tc.status)
			}
			if tc.want == nil {
				return
			}
			var got map[string]string
			if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

// B03: named parameters before a catch-all are captured independently, and the
// catch-all captures the remaining segments.
func TestRouteParamsNamedPlusCatchAll(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/organizations/:orgId/files/+", CatchAll: "path"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error { return c.JSON(RouteParams(c)) }))
	res, err := app.Test(httptest.NewRequest("GET", "/organizations/42/files/reports/2026/summary.pdf", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"orgId": "42", "path": "reports/2026/summary.pdf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

// B03: the request-local parameter map is request-scoped and concurrent-safe
// for catch-all routes.
func TestRouteParamsCatchAllConcurrentIsolation(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	route := routing.Route{Path: "/docs/+", CatchAll: "slug"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error { return c.JSON(RouteParams(c)) }))
	for i := 0; i < 30; i++ {
		i := i
		res, err := app.Test(httptest.NewRequest("GET", "/docs/a/"+strconv.Itoa(i), nil))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got["slug"] != "a/"+strconv.Itoa(i) {
			t.Errorf("request %d: slug=%q", i, got["slug"])
		}
	}
}

// B03: the request-local bridge publishes decoded parameters on the fasthttp
// RequestCtx so a server-rendered templ page can read them from the render
// context.
func TestRouteParamsBridgeToRequestContext(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	route := routing.Route{Path: "/docs/+", CatchAll: "slug"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		// A templ page sees the render context (the fasthttp RequestCtx);
		// ctx.Value(name) must return the decoded catch-all value.
		ctx := c.RequestCtx()
		return c.JSON(map[string]string{"fromCtx": ctx.Value("slug").(string)})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/docs/getting-started/installation", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["fromCtx"] != "getting-started/installation" {
		t.Fatalf("fromCtx=%q", got["fromCtx"])
	}
}

// B03: with the deterministic registration order (static, dynamic, catch-all
// last), a static path, a single-segment dynamic path, and the catch-all all
// dispatch to the correct route. This is the order the framework's sort
// guarantees; a catch-all registered first would steal these URLs.
func TestCatchAllDispatchDeterministicOrder(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	static := routing.Route{Path: "/docs/intro"}
	dyn := routing.Route{Path: "/docs/:id"}
	catch := routing.Route{Path: "/docs/+", CatchAll: "slug"}
	app.Get(static.Path, func(c fiber.Ctx) error { return c.SendString("static") })
	app.Get(dyn.Path, WithRouteParams(dyn, func(c fiber.Ctx) error { return c.SendString("dyn:" + c.Params("id")) }))
	app.Get(catch.Path, WithRouteParams(catch, func(c fiber.Ctx) error { return c.SendString("catch:" + RouteParams(c)["slug"]) }))
	for _, tc := range []struct {
		url, want string
	}{
		{"/docs/intro", "static"},
		{"/docs/foo", "dyn:foo"},
		{"/docs/foo/bar", "catch:foo/bar"},
		{"/docs/foo/bar/baz", "catch:foo/bar/baz"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			res, err := app.Test(httptest.NewRequest("GET", tc.url, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if string(body) != tc.want {
				t.Fatalf("got=%q want=%q", string(body), tc.want)
			}
		})
	}
}
