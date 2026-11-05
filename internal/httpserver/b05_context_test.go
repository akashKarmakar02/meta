package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
	"github.com/gofiber/fiber/v3"
)

func TestMatchedRouteContext(t *testing.T) {
	app := fiber.New(fiber.Config{CaseSensitive: true})
	app.Use(httpserver.ValidatePagePath)
	route := routing.Route{ID: "users/[userId]/posts/[postId]/page.templ", SourceFile: "users/[userId]/posts/[postId]/page.templ", Path: "/users/:userId/posts/:postId"}
	var heldCtx context.Context
	var snapshot routecontext.Route
	app.Get(route.Path, httpserver.WithRouteParams(route, func(c fiber.Ctx) error {
		heldCtx = c.Context()
		var ok bool
		snapshot, ok = routecontext.FromContext(heldCtx)
		if !ok {
			return fmt.Errorf("missing route context")
		}
		legacy := routeparams.FromContext(heldCtx)
		oldID, _ := legacy.Get("userId")
		newID, _ := snapshot.Params.Get("userId")
		if oldID != newID || heldCtx.Err() != nil {
			return fmt.Errorf("incorrect active context")
		}
		return c.JSON(map[string]any{"id": snapshot.RouteID, "pattern": snapshot.Pattern, "user": newID, "post": snapshot.Params.All()["postId"], "query": snapshot.Query.All()})
	}))
	req := httptest.NewRequest("GET", "/users/42/posts/7?userId=wrong&tag=go&tag=web&empty&space=a+b&encoded=%E6%9D%B1%E4%BA%AC", nil)
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var got struct {
		ID, Pattern, User, Post string
		Query                   map[string][]string
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != route.ID || got.Pattern != "/users/[userId]/posts/[postId]" || got.User != "42" || got.Post != "7" {
		t.Fatalf("route=%+v", got)
	}
	if v := got.Query["tag"]; len(v) != 2 || v[0] != "go" || v[1] != "web" || got.Query["space"][0] != "a b" || got.Query["encoded"][0] != "東京" {
		t.Fatalf("query=%v", got.Query)
	}
	if !snapshot.Query.Has("empty") || heldCtx.Err() != context.Canceled {
		t.Fatalf("lifecycle query=%v err=%v", snapshot.Query.All(), heldCtx.Err())
	}
	if snapshot.Query.Get("tag") != "go" {
		t.Fatal("snapshot invalid after dispatch")
	}
}

func TestRouteContextInheritsDeadlineAndCancelsAtReturn(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(parent)
		return c.Next()
	})
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/"}
	var saved context.Context
	app.Get("/", httpserver.WithRouteParams(route, func(c fiber.Ctx) error {
		saved = c.Context()
		matched, ok := routecontext.FromContext(saved)
		if !ok || matched.RouteID != "page.templ" || matched.Pattern != "/" || len(matched.Params.All()) != 0 {
			return fmt.Errorf("static route metadata: %+v", matched)
		}
		if saved == c.RequestCtx() {
			return fmt.Errorf("render context retains pooled request context")
		}
		if _, ok := saved.Deadline(); !ok {
			return fmt.Errorf("parent deadline missing")
		}
		return c.SendStatus(200)
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || saved == nil || saved.Err() != context.Canceled {
		t.Fatalf("status=%d context=%v", res.StatusCode, saved)
	}
	if parent.Err() != nil {
		t.Fatalf("handler cancelled parent: %v", parent.Err())
	}
}

func TestQueryDecodingAndCopies(t *testing.T) {
	app := fiber.New()
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/"}
	app.Get("/", httpserver.WithRouteParams(route, func(c fiber.Ctx) error {
		r, ok := routecontext.FromContext(c.Context())
		if !ok {
			return fmt.Errorf("missing route")
		}
		q := r.Query
		values := q.Values("tag")
		if len(values) != 2 || values[0] != "a b" || values[1] != "c d" || !q.Has("empty") || q.Get("empty") != "" || q.Has("missing") || q.Get("名") != "東京" {
			return fmt.Errorf("incorrect query: %v", q.All())
		}
		values[0] = "mutated"
		all := q.All()
		all["tag"][1] = "mutated"
		if q.Get("tag") != "a b" || q.Values("tag")[1] != "c d" {
			return fmt.Errorf("query mutated")
		}
		return c.SendStatus(200)
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/?tag=a+b&tag=c%20d&empty&%E5%90%8D=%E6%9D%B1%E4%BA%AC", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestRouteContextDispatchOrdering(t *testing.T) {
	app := fiber.New()
	before, after := false, false
	var afterErr error
	app.Use(func(c fiber.Ctx) error {
		_, before = routecontext.FromContext(c.Context())
		err := c.Next()
		_, after = routecontext.FromContext(c.Context())
		afterErr = c.Context().Err()
		return err
	})
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/"}
	app.Get("/", httpserver.WithRouteParams(route, func(c fiber.Ctx) error { return c.SendStatus(200) }))
	res, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if before || !after || afterErr != context.Canceled {
		t.Fatalf("matched: before=%v after=%v err=%v", before, after, afterErr)
	}
	res, err = app.Test(httptest.NewRequest("GET", "/missing", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if before || after || afterErr != nil {
		t.Fatalf("missing: before=%v after=%v err=%v", before, after, afterErr)
	}
	res, err = app.Test(httptest.NewRequest("POST", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if before || after || afterErr != nil {
		t.Fatalf("unsupported method: before=%v after=%v err=%v", before, after, afterErr)
	}
}

func TestRouteContextCatchAllAndQueryErrors(t *testing.T) {
	app := fiber.New(fiber.Config{ReadBufferSize: 16384})
	route := routing.Route{ID: "docs/[...slug]/page.templ", SourceFile: "docs/[...slug]/page.templ", Path: "/docs/+", CatchAll: "slug"}
	app.Get(route.Path, httpserver.WithRouteParams(route, func(c fiber.Ctx) error {
		r, ok := routecontext.FromContext(c.Context())
		if !ok {
			return fmt.Errorf("missing context")
		}
		slug, _ := r.Params.Get("slug")
		return c.JSON(map[string]string{"id": r.RouteID, "pattern": r.Pattern, "slug": slug, "lang": r.Query.Get("lang")})
	}))
	for _, tc := range []struct {
		target string
		status int
		text   string
	}{
		{"/docs/getting-started/install?lang=en", 200, `"slug":"getting-started/install"`},
		{"/docs/foo?bad=%zz", 400, ""},
		{"/docs/foo?a=1;b=2", 400, ""},
		{"/docs/foo?x=" + strings.Repeat("a", 8193), 414, ""},
	} {
		t.Run(tc.target[:min(len(tc.target), 60)], func(t *testing.T) {
			req := httptest.NewRequest("GET", "/docs/foo", nil)
			req.URL.RawQuery = strings.SplitN(tc.target, "?", 2)[1]
			req.URL.Path = strings.SplitN(tc.target, "?", 2)[0]
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf("status=%d want %d", res.StatusCode, tc.status)
			}
			if tc.text != "" {
				var got map[string]string
				if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got["slug"] != "getting-started/install" || got["lang"] != "en" || got["pattern"] != "/docs/[...slug]" {
					t.Fatalf("response=%v", got)
				}
			}
		})
	}
}

func TestConcurrentRouteContextIsolation(t *testing.T) {
	app := fiber.New()
	route := routing.Route{ID: "[id]/page.svelte", SourceFile: "[id]/page.svelte", Path: "/:id"}
	app.Get(route.Path, httpserver.WithRouteParams(route, func(c fiber.Ctx) error {
		r, _ := routecontext.FromContext(c.Context())
		id, _ := r.Params.Get("id")
		return c.SendString(id + ":" + r.Query.Get("q"))
	}))
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value := fmt.Sprint(i)
			res, err := app.Test(httptest.NewRequest("GET", "/"+value+"?q="+value, nil))
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil || string(body) != value+":"+value {
				t.Errorf("got %q err %v", body, err)
			}
		}()
	}
	wg.Wait()
}
