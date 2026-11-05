package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/routeparams"
	"github.com/gofiber/fiber/v3"
)

// B04: the typed parameter API is reachable from the actual render context.
// The handler uses c.RequestCtx() — the same fasthttp RequestCtx passed to
// templ pages during SSR — and reads parameters through routeparams.FromContext,
// not a mock context carrying arbitrary values.

func probeParams(c fiber.Ctx) (routeparams.Params, error) {
	return routeparams.FromContext(c.RequestCtx()), nil
}

// Single dynamic SSR route: one named parameter, read as a string and an int.
func TestB04SingleDynamicSSR(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/users/:userId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		id, _ := params.Get("userId")
		n, err := params.Int64("userId")
		return c.JSON(map[string]any{"userId": id, "n": n, "err": errString(err)})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/users/42", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["userId"] != "42" || got["n"] != float64(42) || got["err"] != "" {
		t.Fatalf("got=%v", got)
	}
}

// Nested dynamic SSR route: two named parameters, both converted.
func TestB04NestedDynamicSSR(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/users/:userId/posts/:postId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		uid, errU := params.Int64("userId")
		pid, errP := params.Int64("postId")
		return c.JSON(map[string]any{
			"userId": uid, "postId": pid,
			"errU": errString(errU), "errP": errString(errP),
		})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/users/42/posts/7", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["userId"] != float64(42) || got["postId"] != float64(7) || got["errU"] != "" || got["errP"] != "" {
		t.Fatalf("got=%v", got)
	}
}

// Invalid numeric conversion is surfaced as an error by the application code,
// not a silent zero.
func TestB04InvalidNumericConversion(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/users/:userId/posts/:postId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		uid, errU := params.Int64("userId")
		return c.JSON(map[string]any{"userId": uid, "errU": errString(errU)})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/users/abc/posts/7", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// Invalid value must not become zero; the error must be non-empty.
	if got["errU"] == "" {
		t.Fatalf("expected an error for invalid userId, got=%v", got)
	}
}

// Catch-all SSR route: the captured value is a slash-joined string.
func TestB04CatchAllSSR(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/docs/+", CatchAll: "slug"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		slug, ok := params.Get("slug")
		return c.JSON(map[string]any{"slug": slug, "ok": ok})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/docs/getting-started/installation", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["slug"] != "getting-started/installation" || got["ok"] != true {
		t.Fatalf("got=%v", got)
	}
}

// Named dynamic segment followed by a catch-all: both are accessible, and the
// named one converts to a number while the catch-all stays a string.
func TestB04NamedPlusCatchAllSSR(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/organizations/:orgId/files/+", CatchAll: "path"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		orgId, errO := params.Int64("orgId")
		path, _ := params.Get("path")
		return c.JSON(map[string]any{"orgId": orgId, "path": path, "errO": errString(errO)})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/organizations/42/files/reports/2026", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["orgId"] != float64(42) || got["path"] != "reports/2026" || got["errO"] != "" {
		t.Fatalf("got=%v", got)
	}
}

// Multiple requests with different values: the request-local collection is
// correct per request (no cross-request leakage through the render context).
func TestB04MultipleRequestsDifferentValues(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/users/:userId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		id, _ := params.Get("userId")
		return c.JSON(map[string]string{"userId": id})
	}))
	for _, id := range []string{"1", "2", "3", "999", "abc"} {
		res, err := app.Test(httptest.NewRequest("GET", "/users/"+id, nil))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got["userId"] != id {
			t.Fatalf("request %s: got=%q", id, got["userId"])
		}
	}
}

// Query strings are not part of the path and never appear in the parameter
// collection.
func TestB04QueryStringNotInParams(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/users/:userId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		id, _ := params.Get("userId")
		_, hasFoo := params.Get("foo")
		return c.JSON(map[string]any{"userId": id, "hasFoo": hasFoo})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/users/42?foo=bar&baz=1", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["userId"] != "42" || got["hasFoo"] != false {
		t.Fatalf("got=%v", got)
	}
}

// Invalid escaped paths are still rejected before routing (B02/B03 invariant),
// so the parameter API never sees a malformed path.
func TestB04InvalidEscapedPathRejected(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/docs/+", CatchAll: "slug"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		slug, _ := params.Get("slug")
		return c.JSON(map[string]string{"slug": slug})
	}))
	for _, url := range []string{"/docs/a%2Fb", "/docs/a%5Cb", "/docs/%2e"} {
		res, err := app.Test(httptest.NewRequest("GET", url, nil))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("%s: status=%d want=400", url, res.StatusCode)
		}
	}
}

// The typed API and the legacy B03 string bridge read the same request-local
// values, so existing pages keep working.
func TestB04LegacyBridgeCompatibility(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/users/:userId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		params, _ := probeParams(c)
		typed, _ := params.Get("userId")
		legacy := RouteParams(c)["userId"]
		return c.JSON(map[string]string{"typed": typed, "legacy": legacy})
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/users/42", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["typed"] != "42" || got["legacy"] != "42" {
		t.Fatalf("got=%v", got)
	}
}

// errString renders an error for JSON assertions without leaking the type.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
