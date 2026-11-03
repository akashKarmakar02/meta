package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go-meta/internal/routing"
)

func TestRouteParamsAndPathSafety(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(ValidatePagePath)
	route := routing.Route{Path: "/org/:orgId/teams/:teamId/members/:memberId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error { return c.JSON(RouteParams(c)) }))
	for _, tc := range []struct {
		name, url string
		status    int
		want      map[string]string
	}{
		{"three parameters", "/org/one/teams/two/members/three?memberId=wrong", 200, map[string]string{"orgId": "one", "teamId": "two", "memberId": "three"}},
		{"independent", "/org/other/teams/sales/members/bob", 200, map[string]string{"orgId": "other", "teamId": "sales", "memberId": "bob"}},
		{"encoded", "/org/%61cme/teams/%E6%9D%B1%E4%BA%AC/members/A%20B", 200, map[string]string{"orgId": "acme", "teamId": "東京", "memberId": "A B"}},
		{"encoded slash", "/org/one%2Ftwo/teams/two/members/three", 400, nil},
		{"encoded backslash", "/org/one%5Ctwo/teams/two/members/three", 400, nil},
		{"encoded dot", "/org/%2e/teams/two/members/three", 400, nil},
		{"encoded dotdot", "/org/%2e%2e/teams/two/members/three", 400, nil},
		{"missing", "/org/one/teams/two/members", 404, nil},
		{"extra", "/org/one/teams/two/members/three/extra", 404, nil},
		{"wrong literal", "/org/one/groups/two/members/three", 404, nil},
		{"wrong case", "/ORG/one/teams/two/members/three", 404, nil},
		{"trailing slash", "/org/one/teams/two/members/three/", 200, map[string]string{"orgId": "one", "teamId": "two", "memberId": "three"}},
		{"repeated separators", "/org/one//teams/two/members/three", 404, nil},
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
			if tc.want != nil {
				var got map[string]string
				if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got=%v want=%v", got, tc.want)
				}
			}
		})
	}
}

func TestRouteParamsConcurrentIsolation(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	route := routing.Route{Path: "/users/:userId/posts/:postId"}
	app.Get(route.Path, WithRouteParams(route, func(c fiber.Ctx) error {
		return c.JSON(RouteParams(c))
	}))
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprint(i)
			res, err := app.Test(httptest.NewRequest("GET", "/users/"+id+"/posts/"+id, nil))
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			var got map[string]string
			if err := json.NewDecoder(res.Body).Decode(&got); err != nil || got["userId"] != id || got["postId"] != id {
				t.Errorf("request %s: params %v, error %v", id, got, err)
			}
		}()
	}
	wg.Wait()
}

func TestSafePagePathMalformedEscapes(t *testing.T) {
	for _, p := range []string{"/users/%", "/users/%0", "/users/%gg", "/users/%ff"} {
		if safePagePath(p) {
			t.Errorf("accepted invalid path %q", p)
		}
	}
}
