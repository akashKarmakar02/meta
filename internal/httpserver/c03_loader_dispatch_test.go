package httpserver_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/loaderdata"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/gofiber/fiber/v3"
)

// renderProbe is a stand-in for the templ page: it reads the loader result
// from the render context and reports what it sees, so the tests can prove
// the typed result is delivered to the page.
func renderProbe(c fiber.Ctx) error {
	post, ok := loaderdata.Get[postResult](c.Context())
	if !ok {
		return c.SendString("absent")
	}
	return c.SendString(fmt.Sprintf("id=%d title=%s", post.ID, post.Title))
}

type postResult struct {
	ID    int64
	Title string
}

func TestWithLoaderExecutesSSRLoaderOnceAndAttachesResult(t *testing.T) {
	var calls atomic.Int32
	reg, err := loader.NewRegistry([]string{"page.templ"},
		loader.Bind("page.templ", func(_ context.Context, r routecontext.Route) (postResult, error) {
			calls.Add(1)
			id, _ := r.Params.Int64("id")
			return postResult{ID: id, Title: "Hello"}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/:id", Mode: routing.ModeSSR}
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, renderProbe)))

	res, err := app.Test(httptest.NewRequest("GET", "/42", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "id=42 title=Hello" {
		t.Fatalf("status=%d body=%q", res.StatusCode, body)
	}
	if calls.Load() != 1 {
		t.Fatalf("loader ran %d times, want 1", calls.Load())
	}
}

func TestWithLoaderSkipsRouteWithoutBinding(t *testing.T) {
	var calls atomic.Int32
	reg, err := loader.NewRegistry([]string{"page.templ"},
		loader.Bind("page.templ", func(context.Context, routecontext.Route) (postResult, error) {
			calls.Add(1)
			return postResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	// A different route with no binding.
	route := routing.Route{ID: "other/page.templ", SourceFile: "other/page.templ", Path: "/other", Mode: routing.ModeSSR}
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, renderProbe)))

	res, err := app.Test(httptest.NewRequest("GET", "/other", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "absent" {
		t.Fatalf("status=%d body=%q", res.StatusCode, body)
	}
	if calls.Load() != 0 {
		t.Fatalf("loader ran %d times, want 0", calls.Load())
	}
}

// TestWithLoaderExecutesCSRRouteAndSerializesJSON is the C04 counterpart to
// the C03 "CSR does not execute" test: CSR loaders now run once and their
// result is JSON-serialized onto the context for browser delivery.
func TestWithLoaderExecutesCSRRouteAndSerializesJSON(t *testing.T) {
	var calls atomic.Int32
	reg, err := loader.NewRegistry([]string{"page.svelte"},
		loader.Bind("page.svelte", func(context.Context, routecontext.Route) (postResult, error) {
			calls.Add(1)
			return postResult{ID: 5, Title: "Team"}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route{ID: "page.svelte", SourceFile: "page.svelte", Path: "/csr", Mode: routing.ModeCSR}
	var captured []byte
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, func(c fiber.Ctx) error {
		if data, ok := httpserver.CSRLoaderData(c.Context()); ok {
			captured = data
		}
		return c.SendString("ok")
	})))

	res, err := app.Test(httptest.NewRequest("GET", "/csr", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("CSR loader ran %d times, want 1", calls.Load())
	}
	if string(captured) != `{"ID":5,"Title":"Team"}` {
		t.Fatalf("serialized CSR data=%q", captured)
	}
}

// TestWithLoaderCSRSerializationFailureStopsRender proves an unsupported
// result type (a function) prevents a successful CSR response rather than
// publishing a page with missing data.
func TestWithLoaderCSRSerializationFailureStopsRender(t *testing.T) {
	reg, err := loader.NewRegistry([]string{"page.svelte"},
		loader.Bind("page.svelte", func(context.Context, routecontext.Route) (func(), error) {
			return func() {}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route{ID: "page.svelte", SourceFile: "page.svelte", Path: "/csr", Mode: routing.ModeCSR}
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, func(c fiber.Ctx) error {
		return c.SendString("should not render")
	})))
	res, err := app.Test(httptest.NewRequest("GET", "/csr", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 500 {
		t.Fatalf("status=%d, want 500 (serialization failure must stop render)", res.StatusCode)
	}
	if string(body) == "should not render" {
		t.Fatalf("page rendered despite serialization failure")
	}
}

func TestWithLoaderNilRegistryIsNoop(t *testing.T) {
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/x", Mode: routing.ModeSSR}
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(nil, route, renderProbe)))
	res, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "absent" {
		t.Fatalf("status=%d body=%q", res.StatusCode, body)
	}
}

func TestWithLoaderFailureStopsRenderAndHidesError(t *testing.T) {
	reg, err := loader.NewRegistry([]string{"page.templ"},
		loader.Bind("page.templ", func(context.Context, routecontext.Route) (postResult, error) {
			return postResult{}, errors.New("secret database failure")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/fail", Mode: routing.ModeSSR}
	app := fiber.New(fiber.Config{ErrorHandler: renderHTTPErrorHandler})
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, renderProbe)))

	res, err := app.Test(httptest.NewRequest("GET", "/fail", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 500 {
		t.Fatalf("status=%d, want 500", res.StatusCode)
	}
	if strings.Contains(string(body), "secret database failure") {
		t.Fatalf("raw loader error leaked: %s", body)
	}
}

func TestWithLoaderPassesCorrectSnapshot(t *testing.T) {
	var got routecontext.Route
	reg, err := loader.NewRegistry([]string{"users/[userId]/posts/[postId]/page.templ"},
		loader.Bind("users/[userId]/posts/[postId]/page.templ", func(_ context.Context, r routecontext.Route) (postResult, error) {
			got = r
			return postResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route{ID: "users/[userId]/posts/[postId]/page.templ", SourceFile: "users/[userId]/posts/[postId]/page.templ", Path: "/users/:userId/posts/:postId", Mode: routing.ModeSSR}
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, renderProbe)))

	res, err := app.Test(httptest.NewRequest("GET", "/users/42/posts/7?tag=a&tag=b", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got.RouteID != route.ID {
		t.Fatalf("route ID=%q", got.RouteID)
	}
	if uid, _ := got.Params.Int64("userId"); uid != 42 {
		t.Fatalf("userId=%d", uid)
	}
	if pid, _ := got.Params.Int64("postId"); pid != 7 {
		t.Fatalf("postId=%d", pid)
	}
	if tags := got.Query.Values("tag"); len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("query=%v", tags)
	}
}

func TestWithLoaderRenderContextIsNotPooledRequest(t *testing.T) {
	reg, err := loader.NewRegistry([]string{"page.templ"},
		loader.Bind("page.templ", func(context.Context, routecontext.Route) (postResult, error) {
			return postResult{ID: 1}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/p", Mode: routing.ModeSSR}
	var renderCtx context.Context
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, func(c fiber.Ctx) error {
		renderCtx = c.Context()
		if c.Context() == c.RequestCtx() {
			return errors.New("render context is the pooled request context")
		}
		return c.SendStatus(200)
	})))
	res, err := app.Test(httptest.NewRequest("GET", "/p", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	// The loader result is readable from the render context.
	if _, ok := loaderdata.Get[postResult](renderCtx); !ok {
		t.Fatal("loader result not readable from render context")
	}
}

func TestWithLoaderConcurrentIsolation(t *testing.T) {
	var calls atomic.Int32
	reg, err := loader.NewRegistry([]string{"page.templ"},
		loader.Bind("page.templ", func(_ context.Context, r routecontext.Route) (postResult, error) {
			calls.Add(1)
			id, _ := r.Params.Int64("id")
			return postResult{ID: id}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/:id", Mode: routing.ModeSSR}
	app := fiber.New()
	app.Get(route.Path, httpserver.WithRouteParams(route, httpserver.WithLoader(reg, route, renderProbe)))

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := app.Test(httptest.NewRequest("GET", fmt.Sprintf("/%d", i), nil))
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if string(body) != fmt.Sprintf("id=%d title=", i) {
				t.Errorf("got %q, want id=%d", body, i)
			}
		}(i)
	}
	wg.Wait()
	if calls.Load() != 30 {
		t.Fatalf("loader ran %d times, want 30", calls.Load())
	}
}

// renderHTTPErrorHandler is a minimal centralized handler for the failure test:
// it maps a 5xx fiber.Error to a generic body that never includes the cause.
func renderHTTPErrorHandler(c fiber.Ctx, err error) error {
	var ferr *fiber.Error
	if errors.As(err, &ferr) && ferr.Code >= 500 {
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Status(ferr.Code).SendString("<h1>500 — Internal Server Error</h1>")
	}
	return fiber.DefaultErrorHandler(c, err)
}
