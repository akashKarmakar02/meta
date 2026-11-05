package httpserver_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/gofiber/fiber/v3"
)

func TestB06WinningRouteContext(t *testing.T) {
	fsys := fstest.MapFS{
		"products/new/page.templ": {}, "products/[id]/page.svelte": {}, "products/[...tail]/page.templ": {},
		"users/[z]/settings/page.templ": {}, "users/[a]/[section]/page.svelte": {},
		"a/static/[...tail]/page.templ": {}, "a/[id]/[name]/page.svelte": {},
		"unicode/é/page.templ": {}, "unicode/[id]/page.svelte": {},
	}
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(httpserver.ValidatePagePath)
	for _, r := range routes {
		r := r
		app.Get(routing.FiberRegistrationPath(r.Path), httpserver.WithRouteParams(r, func(c fiber.Ctx) error {
			route, ok := routecontext.FromContext(c.Context())
			if !ok {
				t.Fatal("missing route context")
			}
			return c.JSON(map[string]any{"id": route.RouteID, "pattern": route.Pattern, "params": route.Params.All(), "query": route.Query.Get("q")})
		}))
	}
	tests := []struct {
		path, id, pattern, param, value string
		status                          int
	}{
		{"/products/new?q=hello", "products/new/page.templ", "/products/new", "", "", 200},
		{"/products/42?q=hello", "products/[id]/page.svelte", "/products/[id]", "id", "42", 200},
		{"/products/a/b?q=hello", "products/[...tail]/page.templ", "/products/[...tail]", "tail", "a/b", 200},
		{"/users/7/settings?q=hello", "users/[z]/settings/page.templ", "/users/[z]/settings", "z", "7", 200},
		{"/a/static/x?q=hello", "a/static/[...tail]/page.templ", "/a/static/[...tail]", "tail", "x", 200},
		{"/unicode/%C3%A9?q=hello", "unicode/é/page.templ", "/unicode/é", "", "", 200},
		{"/products/a%2Fb", "", "", "", "", 400},
		{"/missing", "", "", "", "", 404},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			res, err := app.Test(httptest.NewRequest("GET", tt.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tt.status {
				t.Fatalf("status=%d want %d", res.StatusCode, tt.status)
			}
			if tt.status != 200 {
				return
			}
			var got struct {
				ID, Pattern, Query string
				Params             map[string]string
			}
			if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.ID != tt.id || got.Pattern != tt.pattern || got.Query != "hello" || tt.param != "" && got.Params[tt.param] != tt.value {
				t.Fatalf("route context: %+v", got)
			}
		})
	}
}
