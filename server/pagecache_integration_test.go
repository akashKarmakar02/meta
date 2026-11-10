package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/routecontext"

	"github.com/a-h/templ"

	"github.com/akashKarmakar02/meta/cache"
	pagecache "github.com/akashKarmakar02/meta/pagecache"
	"github.com/akashKarmakar02/meta/routing"
)

func TestServerPageCache(t *testing.T) {
	var renders atomic.Int64
	resolver := func(r routing.Route) (templ.Component, error) {
		n := renders.Add(1)
		return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
			_, err := fmt.Fprintf(w, "<main>render-%d %s</main>", n, r.ID)
			return err
		}), nil
	}
	routes := []routing.Route{
		{ID: "page.templ", SourceFile: "page.templ", Path: "/", Mode: routing.ModeSSR},
	}
	mstore, merr := cache.NewMemory(cache.MemoryConfig{MaxEntries: 100, MaxBytes: 1 << 20})
	if merr != nil {
		t.Fatal(merr)
	}
	pc := pagecache.NewPageCache(mstore, time.Minute, pagecache.PublicRoutes("page.templ"))
	app, err := New(Config{
		Routes: routes, SSRPage: resolver, BarePage: resolver,
		VisualLayout: func(_ routing.Route, c templ.Component) (templ.Component, error) { return c, nil },
		PageCache:    pc,
	})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) (*http.Response, string) {
		t.Helper()
		res, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, string(body)
	}
	res, first := get("/")
	if res.StatusCode != 200 || !strings.Contains(first, "render-1") {
		t.Fatalf("first: %d %s", res.StatusCode, first)
	}
	res, second := get("/")
	if res.StatusCode != 200 || second != first {
		t.Fatalf("second request not served from cache: %d %s", res.StatusCode, second)
	}
	if renders.Load() != 1 {
		t.Fatalf("renderer ran %d times for identical requests", renders.Load())
	}
	if err := pc.Invalidate(context.Background(), routecontext.Route{Pattern: "/"}); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	_, third := get("/")
	if !strings.Contains(third, "render-2") {
		t.Fatalf("post-invalidation body: %s", third)
	}
	if renders.Load() != 2 {
		t.Fatalf("renderer ran %d times after invalidation", renders.Load())
	}
}

func TestServerPageCacheSkipsCSR(t *testing.T) {
	var renders atomic.Int64
	resolver := func(r routing.Route) (templ.Component, error) {
		n := renders.Add(1)
		return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
			_, err := fmt.Fprintf(w, "<main>render-%d</main>", n)
			return err
		}), nil
	}
	routes := []routing.Route{
		{ID: "dashboard/page.svelte", SourceFile: "dashboard/page.svelte", Path: "/dashboard", Mode: routing.ModeCSR},
	}
	mstore, merr := cache.NewMemory(cache.MemoryConfig{MaxEntries: 100, MaxBytes: 1 << 20})
	if merr != nil {
		t.Fatal(merr)
	}
	pc := pagecache.NewPageCache(mstore, time.Minute)
	client := t.TempDir()
	files := map[string]string{
		".vite/manifest.json": `{"frontend/entry.ts":{"file":"assets/entry-a.js","isEntry":true}}`,
		"assets/entry-a.js":   "console.log('entry')",
	}
	for name, data := range files {
		p := filepath.Join(client, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app, err := New(Config{Routes: routes, BarePage: resolver,
		VisualLayout: func(_ routing.Route, c templ.Component) (templ.Component, error) { return c, nil },
		PageCache:    pc, ClientDir: client})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		res, err := app.Test(httptest.NewRequest("GET", "/dashboard", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if !strings.Contains(string(body), `data-go-route="/dashboard"`) {
			t.Fatalf("CSR page render %d unexpected: %s", i, body)
		}
	}
}
