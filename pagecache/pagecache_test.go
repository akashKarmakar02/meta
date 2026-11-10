package pagecache

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/gofiber/fiber/v3"
)

// newCacheMemory builds a usable Memory store or fails the test.
func newCacheMemory(t *testing.T) *cache.Memory {
	t.Helper()
	m, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 64, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	return m
}

// newPageCacheFixture wires the cache wrapper inside WithRouteParams, the
// way server.New composes page handlers, so Route is present in context.
func newPageCacheFixture(t *testing.T) (*fiber.App, *PageCache, *int) {
	t.Helper()
	store := newCacheMemory(t)
	pc := NewPageCache(store, time.Minute)
	count := 0
	app := fiber.New(fiber.Config{CaseSensitive: true})
	handler := pc.Wrap(func(c fiber.Ctx) error {
		count++
		c.Set("X-Render-Count", strconv.Itoa(count))
		return c.SendString("render-" + strconv.Itoa(count))
	})
	app.Get("/x", httpserver.WithRouteParams(routing.Route{ID: "p/x", Path: "/x"}, handler))
	app.Get("/p", httpserver.WithRouteParams(routing.Route{ID: "p/p", Path: "/p"}, handler))
	return app, pc, &count
}

func TestPageCacheSecondGETIsServedFromStore(t *testing.T) {
	f, pc, count := newPageCacheFixture(t)
	r1, err := f.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	r1.Body.Close()
	r2, err := f.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	buf := make([]byte, 64)
	n, _ := r2.Body.Read(buf)
	if got := string(buf[:n]); got != "render-1" {
		t.Fatalf("second GET should hit cache, got %q", got)
	}
	if *count != 1 {
		t.Fatalf("handler should have rendered once, rendered %d", *count)
	}
	_ = pc
}

func TestPageCacheDifferentQueryIsSeparateEntry(t *testing.T) {
	f, _, _ := newPageCacheFixture(t)
	for _, uri := range []string{"/p?tag=one", "/p?tag=two"} {
		res, err := f.Test(httptest.NewRequest("GET", uri, nil))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	res, err := f.Test(httptest.NewRequest("GET", "/p?tag=one", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, 64)
	n, _ := res.Body.Read(buf)
	if got := string(buf[:n]); got != "render-1" {
		t.Fatalf("same query should reuse first render, got %q", got)
	}
}

func TestPageCacheInvalidateForcesRerender(t *testing.T) {
	f, pc, count := newPageCacheFixture(t)
	res, _ := f.Test(httptest.NewRequest("GET", "/x", nil))
	res.Body.Close()
	res, _ = f.Test(httptest.NewRequest("GET", "/x", nil))
	res.Body.Close()
	route := routecontext.Route{Pattern: "/x"}
	if err := pc.Invalidate(context.Background(), route); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	res2, _ := f.Test(httptest.NewRequest("GET", "/x", nil))
	defer res2.Body.Close()
	buf := make([]byte, 64)
	n, _ := res2.Body.Read(buf)
	if got := string(buf[:n]); got != "render-2" {
		t.Fatalf("after invalidate a fresh render should run, got %q", got)
	}
	if *count != 2 {
		t.Fatalf("expected 2 total renders (initial + post-invalidate), got %d", *count)
	}
}

func TestPageCacheInvalidatePathForcesRerender(t *testing.T) {
	f, pc, count := newPageCacheFixture(t)
	res, _ := f.Test(httptest.NewRequest("GET", "/x", nil))
	res.Body.Close()
	// The path-based form is the programmatic API apps call after mutations.
	if err := pc.InvalidatePath(context.Background(), "/x"); err != nil {
		t.Fatalf("invalidate path: %v", err)
	}
	res2, _ := f.Test(httptest.NewRequest("GET", "/x", nil))
	defer res2.Body.Close()
	buf := make([]byte, 64)
	n, _ := res2.Body.Read(buf)
	if got := string(buf[:n]); got != "render-2" {
		t.Fatalf("after invalidate-path a fresh render should run, got %q", got)
	}
	if *count != 2 {
		t.Fatalf("expected 2 total renders, got %d", *count)
	}
}

func TestPageCacheInvalidatePathRejectsBadPath(t *testing.T) {
	_, pc, _ := newPageCacheFixture(t)
	if err := pc.InvalidatePath(context.Background(), ""); err == nil {
		t.Fatal("empty path must be rejected")
	}
	// A trailing slash names the same page; it must invalidate cleanly too.
	f, pc2, count := newPageCacheFixture(t)
	res, _ := f.Test(httptest.NewRequest("GET", "/x", nil))
	res.Body.Close()
	if err := pc2.InvalidatePath(context.Background(), "/x/"); err != nil {
		t.Fatalf("trailing slash should be normalized: %v", err)
	}
	res2, _ := f.Test(httptest.NewRequest("GET", "/x", nil))
	defer res2.Body.Close()
	buf := make([]byte, 64)
	n, _ := res2.Body.Read(buf)
	if got := string(buf[:n]); got != "render-2" || *count != 2 {
		t.Fatalf("normalized invalidate should force re-render, got %q count=%d", got, *count)
	}
}
