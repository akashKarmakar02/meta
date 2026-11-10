package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/loaderdata"
	"github.com/akashKarmakar02/meta/metadata"
	"github.com/akashKarmakar02/meta/observability"
	"github.com/akashKarmakar02/meta/pagecache"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routing"
	"github.com/gofiber/fiber/v3"
)

const dualRouteID = "blog/[slug]/page.templ"
const dualPath = "/blog/example?view=full"

type dualFixture struct {
	loads, pages, layouts atomic.Int64
	value                 atomic.Value
	load                  func(context.Context, routecontext.Route) (string, error)
	failPage, failLayout  atomic.Bool
}

func newDualFixture() *dualFixture {
	f := &dualFixture{}
	f.value.Store("original")
	return f
}

func dualStore(t *testing.T) cache.Store {
	t.Helper()
	s, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 100, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *dualFixture) config(pc *pagecache.PageCache) Config {
	route := routing.Route{ID: dualRouteID, SourceFile: dualRouteID, Path: "/blog/:slug", Mode: routing.ModeSSR}
	bare := func(routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			f.pages.Add(1)
			if f.failPage.Load() {
				return errors.New("page failed")
			}
			data, ok := loaderdata.Get[string](ctx)
			if !ok {
				return errors.New("typed loader result missing")
			}
			r, _ := routecontext.FromContext(ctx)
			slug, _ := r.Params.Get("slug")
			metadata.Set(ctx, metadata.Page{Title: data, Canonical: "/blog/" + slug, OpenGraph: metadata.OpenGraph{Title: data}, Twitter: metadata.Twitter{Card: "summary"}})
			_, err := fmt.Fprintf(w, "<main><h1>%s</h1><p>%s|%s</p></main>", data, slug, r.Query.Get("view"))
			return err
		}), nil
	}
	visual := func(_ routing.Route, child templ.Component) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			f.layouts.Add(1)
			if f.failLayout.Load() {
				return errors.New("layout failed")
			}
			_, _ = io.WriteString(w, "<section>")
			if err := child.Render(ctx, w); err != nil {
				return err
			}
			metadata.Set(ctx, metadata.Page{Title: "layout default", Description: "inherited description"})
			_, err := io.WriteString(w, "</section>")
			return err
		}), nil
	}
	return Config{
		Routes: []routing.Route{route}, BarePage: bare, VisualLayout: visual,
		SSRPage: func(r routing.Route) (templ.Component, error) {
			page, err := bare(r)
			if err != nil {
				return nil, err
			}
			return visual(r, metadata.PageComponent(page))
		},
		Loaders: []loader.Binding{loader.Bind(dualRouteID, func(ctx context.Context, r routecontext.Route) (string, error) {
			f.loads.Add(1)
			if f.load != nil {
				return f.load(ctx, r)
			}
			return f.value.Load().(string), nil
		})}, PageCache: pc,
	}
}

func dualApp(t *testing.T, cfg Config) *fiber.App {
	t.Helper()
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown() })
	return app
}

func dualRequest(app *fiber.App, nav bool, path string, headers http.Header) (*http.Response, []byte, error) {
	req := httptest.NewRequest("GET", path, nil)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if nav {
		req.Header.Set(httpserver.NavigationHeader, "1")
	}
	res, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	return res, body, err
}

func assertDual(t *testing.T, app *fiber.App, nav bool, value string) []byte {
	t.Helper()
	res, body, err := dualRequest(app, nav, dualPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if nav {
		if res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("navigation headers: %v", res.Header)
		}
		var env httpserver.NavigationResponse
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatal(err)
		}
		if env.Version != 1 || env.URL != dualPath || env.RouteID != dualRouteID || env.Mode != "ssr" || env.Title != value || env.Metadata == nil || env.Metadata.Description != "inherited description" || env.Metadata.Canonical != "/blog/example" || env.Metadata.OpenGraph.Title != value || env.Metadata.Twitter.Card != "summary" || !strings.Contains(env.HTML, "<section><main><h1>"+value) {
			t.Fatalf("envelope: %+v", env)
		}
	} else {
		if res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("HTML headers: %v", res.Header)
		}
		for _, want := range []string{"<!DOCTYPE html>", "<title>" + value + "</title>", "<section><main><h1>" + value, `content="inherited description"`, `href="/blog/example"`, `content="summary"`, "example|full"} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("missing %q: %s", want, body)
			}
		}
	}
	return body
}

func TestDualPageCacheRequestOrderAndParity(t *testing.T) {
	for _, firstNav := range []bool{false, true} {
		t.Run(fmt.Sprint("navigation-first=", firstNav), func(t *testing.T) {
			f := newDualFixture()
			pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
			app := dualApp(t, f.config(pc))
			assertDual(t, app, firstNav, "original")
			if f.loads.Load() != 1 || f.pages.Load() != 1 || f.layouts.Load() != 1 {
				t.Fatal("first request must eagerly render page and layout once")
			}
			assertDual(t, app, !firstNav, "original")
			assertDual(t, app, firstNav, "original")
			if f.loads.Load() != 1 || f.pages.Load() != 1 || f.layouts.Load() != 1 {
				t.Fatalf("loads/pages/layouts: %d/%d/%d", f.loads.Load(), f.pages.Load(), f.layouts.Load())
			}
			uncached := dualApp(t, f.config(nil))
			for _, nav := range []bool{false, true} {
				cached := assertDual(t, app, nav, "original")
				fresh := assertDual(t, uncached, nav, "original")
				if string(cached) != string(fresh) {
					t.Fatalf("cached/uncached parity failed: %s vs %s", cached, fresh)
				}
			}
		})
	}
}

func TestDualPageCacheDiskRestartAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	f := newDualFixture()
	open := func() (*cache.Disk, *pagecache.PageCache, *fiber.App) {
		s, err := cache.NewDiskStore(cache.DiskConfig{Dir: dir, MaxEntryBytes: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		pc := pagecache.NewPageCache(s, cache.Never, pagecache.PublicRoutes(dualRouteID), pagecache.BuildID("test-release"))
		return s, pc, dualApp(t, f.config(pc))
	}
	s, pc, app := open()
	legacy, _ := cache.NewKey("page", "/blog/[slug]", "slug=example", "q:view=full")
	if err := s.Set(context.Background(), legacy, cache.Entry{Value: []byte("text/html\x00beta.5 stale document")}, cache.Policy{TTL: cache.Never}); err != nil {
		t.Fatal(err)
	}
	assertDual(t, app, false, "original")
	assertDual(t, app, true, "original")
	f.value.Store("updated")
	if err := pc.InvalidatePath(context.Background(), "/blog/example"); err != nil {
		t.Fatal(err)
	}
	assertDual(t, app, true, "updated")
	assertDual(t, app, false, "updated")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, pc, app = open()
	assertDual(t, app, false, "updated")
	assertDual(t, app, true, "updated")
	if f.loads.Load() != 2 {
		t.Fatalf("restart regenerated cached page: %d", f.loads.Load())
	}
	// Invalidate before this process has served another path/query variant.
	f.value.Store("after restart")
	if err := pc.InvalidatePath(context.Background(), "/blog/example"); err != nil {
		t.Fatal(err)
	}
	assertDual(t, app, false, "after restart")
	assertDual(t, app, true, "after restart")
	if f.loads.Load() != 3 {
		t.Fatalf("expected 3 generations, got %d", f.loads.Load())
	}
	_ = s.Close()
	// The changed path token itself must survive a second restart.
	s, _, app = open()
	defer s.Close()
	assertDual(t, app, true, "after restart")
	if f.loads.Load() != 3 {
		t.Fatal("invalidation token did not persist")
	}
}

func TestDualPageCacheConcurrentMixedMisses(t *testing.T) {
	f := newDualFixture()
	started, release := make(chan struct{}), make(chan struct{})
	f.load = func(ctx context.Context, _ routecontext.Route) (string, error) {
		if f.loads.Load() == 1 {
			close(started)
		}
		select {
		case <-release:
			return "snapshot", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	metrics := observability.NewCollector()
	pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
	cfg := f.config(pc)
	cfg.Observability = &observability.Config{Metrics: metrics}
	app := dualApp(t, cfg)
	const requests = 20
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, body, err := dualRequest(app, i%2 == 1, dualPath, nil)
			if err == nil && (res.StatusCode != 200 || !strings.Contains(string(body), "snapshot")) {
				err = fmt.Errorf("mixed response: %d %s", res.StatusCode, body)
			}
			errs <- err
		}()
	}
	<-started
	deadline := time.After(5 * time.Second)
	for {
		var coalesced uint64
		for _, m := range metrics.Snapshot() {
			if m.Kind == "pagecache" && m.Outcome == "coalesced" {
				coalesced += m.Count
			}
		}
		if coalesced == requests-1 {
			break
		}
		select {
		case <-deadline:
			close(release)
			wg.Wait()
			t.Fatal("requests did not join shared flight")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertDual(t, app, false, "snapshot")
	assertDual(t, app, true, "snapshot")
	if f.loads.Load() != 1 || f.pages.Load() != 1 || f.layouts.Load() != 1 {
		t.Fatalf("generation counts %d/%d/%d", f.loads.Load(), f.pages.Load(), f.layouts.Load())
	}
}

func TestDualPageCacheInvalidationFencesSlowGeneration(t *testing.T) {
	f := newDualFixture()
	started, release := make(chan struct{}), make(chan struct{})
	f.load = func(ctx context.Context, _ routecontext.Route) (string, error) {
		value := f.value.Load().(string)
		if value == "original" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return value, nil
	}
	pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
	app := dualApp(t, f.config(pc))
	done := make(chan error, 1)
	go func() { _, _, err := dualRequest(app, false, dualPath, nil); done <- err }()
	<-started
	f.value.Store("updated")
	if err := pc.InvalidatePath(context.Background(), "/blog/example"); err != nil {
		t.Fatal(err)
	}
	assertDual(t, app, true, "updated") // must not join the obsolete flight
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertDual(t, app, false, "updated")
	assertDual(t, app, true, "updated")
	if f.loads.Load() != 2 {
		t.Fatalf("expected two generations, got %d", f.loads.Load())
	}
}

func TestDualPageCacheSecurity(t *testing.T) {
	for _, header := range []string{"Cookie", "Authorization", "Proxy-Authorization"} {
		t.Run(header, func(t *testing.T) {
			f := newDualFixture()
			pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
			type identityKey struct{}
			f.load = func(ctx context.Context, _ routecontext.Route) (string, error) {
				if user, ok := ctx.Value(identityKey{}).(string); ok && user != "" {
					return user, nil
				}
				return "public", nil
			}
			cfg := f.config(pc)
			cfg.Middleware = []fiber.Handler{func(c fiber.Ctx) error {
				c.SetContext(context.WithValue(c.Context(), identityKey{}, c.Get(header)))
				return c.Next()
			}}
			app := dualApp(t, cfg)
			assertDual(t, app, false, "public")
			for _, nav := range []bool{false, true} {
				res, body, err := dualRequest(app, nav, dualPath, http.Header{header: {"secret-user"}})
				if err != nil || res.StatusCode != 200 || !strings.Contains(string(body), "secret-user") {
					t.Fatalf("personalized request: %v %s", err, body)
				}
			}
			assertDual(t, app, true, "public")
			if f.loads.Load() != 3 {
				t.Fatalf("personalized requests must bypass lookup/write, loads=%d", f.loads.Load())
			}
		})
	}
	for _, tc := range []struct{ name, header, value string }{
		{"cookie", "Set-Cookie", "session=secret; HttpOnly"}, {"private", "Cache-Control", "private"}, {"no-store", "Cache-Control", "no-store"}, {"nonce", "Content-Security-Policy", "script-src 'nonce-secret'"}, {"vary", "Vary", "X-Tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDualFixture()
			pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
			cfg := f.config(pc)
			// Set during loader execution, exercising write-time validation.
			type requestKey struct{}
			cfg.Middleware = []fiber.Handler{func(c fiber.Ctx) error {
				c.SetContext(context.WithValue(c.Context(), requestKey{}, c))
				return c.Next()
			}}
			f.load = func(ctx context.Context, _ routecontext.Route) (string, error) {
				ctx.Value(requestKey{}).(fiber.Ctx).Set(tc.header, tc.value)
				return "original", nil
			}
			app := dualApp(t, cfg)
			for _, nav := range []bool{false, true, false} {
				assertDual(t, app, nav, "original")
			}
			if f.loads.Load() != 3 {
				t.Fatalf("unsafe response cached: %d", f.loads.Load())
			}
		})
	}
	t.Run("protected-policy-runs-on-hit", func(t *testing.T) {
		f := newDualFixture()
		pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
		// Populate publicly, then mount the same cache behind authorization.
		assertDual(t, dualApp(t, f.config(pc)), false, "original")
		var checks atomic.Int64
		cfg := f.config(pc)
		cfg.Policies = []Policy{Protect("/blog/*", func(c fiber.Ctx) error {
			checks.Add(1)
			if c.Get("X-Allow") == "yes" {
				return c.Next()
			}
			return fiber.ErrForbidden
		})}
		app := dualApp(t, cfg)
		for _, nav := range []bool{false, true} {
			res, _, err := dualRequest(app, nav, dualPath, nil)
			if err != nil || res.StatusCode != 403 {
				t.Fatalf("policy bypassed: %v", err)
			}
		}
		for range 2 {
			res, _, err := dualRequest(app, false, dualPath, http.Header{"X-Allow": {"yes"}})
			if err != nil || res.StatusCode != 200 {
				t.Fatal("authorized request failed")
			}
		}
		if checks.Load() != 4 || f.loads.Load() != 3 {
			t.Fatalf("policy checks=%d loads=%d", checks.Load(), f.loads.Load())
		}
	})
	t.Run("default-disabled", func(t *testing.T) {
		f := newDualFixture()
		app := dualApp(t, f.config(pagecache.NewPageCache(dualStore(t), cache.Never)))
		assertDual(t, app, false, "original")
		assertDual(t, app, true, "original")
		if f.loads.Load() != 2 {
			t.Fatal("public route opt-in is required")
		}
	})
}

func TestDualPageCacheFailedGenerationRecovers(t *testing.T) {
	for _, stage := range []string{"page", "layout", "loader", "redirect"} {
		t.Run(stage, func(t *testing.T) {
			f := newDualFixture()
			pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
			switch stage {
			case "page":
				f.failPage.Store(true)
			case "layout":
				f.failLayout.Store(true)
			case "loader":
				f.load = func(context.Context, routecontext.Route) (string, error) { return "", loader.ErrNotFound }
			case "redirect":
				f.load = func(context.Context, routecontext.Route) (string, error) { return "", loader.Redirect("/login") }
			}
			app := dualApp(t, f.config(pc))
			for _, nav := range []bool{false, true} {
				res, _, err := dualRequest(app, nav, dualPath, nil)
				if err != nil {
					t.Fatal(err)
				}
				want := 500
				if stage == "loader" {
					want = 404
				}
				if stage == "redirect" {
					want = 302
				}
				if res.StatusCode != want {
					t.Fatalf("status=%d want=%d", res.StatusCode, want)
				}
			}
			f.failPage.Store(false)
			f.failLayout.Store(false)
			f.load = nil
			assertDual(t, app, true, "original")
			assertDual(t, app, false, "original")
			if f.loads.Load() != 3 {
				t.Fatalf("failed generation poisoned pair: %d", f.loads.Load())
			}
		})
	}
}

func TestDualPageCacheURLVariantsAndDeploymentScope(t *testing.T) {
	f := newDualFixture()
	store := dualStore(t)
	pc := pagecache.NewPageCache(store, cache.Never, pagecache.PublicRoutes(dualRouteID), pagecache.BuildID("release-a"))
	app := dualApp(t, f.config(pc))
	paths := []string{"/blog/example?view=full&x=1", "/blog/example?x=1&view=full", "/blog/%65xample?view=full&x=1"}
	for _, path := range paths {
		_, body, err := dualRequest(app, true, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		var env httpserver.NavigationResponse
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatal(err)
		}
		if env.URL != path {
			t.Fatalf("URL alias mismatch: %s vs %s", env.URL, path)
		}
	}
	if f.loads.Load() != 3 {
		t.Fatal("exact URLs must have distinct envelope identities")
	}
	f.value.Store("updated")
	if err := pc.InvalidatePath(context.Background(), "/blog/example"); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		_, body, err := dualRequest(app, true, path, nil)
		if err != nil || !strings.Contains(string(body), "updated") {
			t.Fatalf("variant not invalidated: %v %s", err, body)
		}
	}
	if f.loads.Load() != 6 {
		t.Fatal("all variants must invalidate together")
	}
	f.value.Store("release-b content")
	pc2 := pagecache.NewPageCache(store, cache.Never, pagecache.PublicRoutes(dualRouteID), pagecache.BuildID("release-b"))
	app2 := dualApp(t, f.config(pc2))
	_, body, err := dualRequest(app2, false, paths[0], nil)
	if err != nil || !strings.Contains(string(body), "release-b content") {
		t.Fatalf("deployment reused old content: %v %s", err, body)
	}
}

func TestDualPageCachePreservesHeadersWithoutReplayingRequestID(t *testing.T) {
	f := newDualFixture()
	pc := pagecache.NewPageCache(dualStore(t), cache.Never, pagecache.PublicRoutes(dualRouteID))
	type requestKey struct{}
	cfg := f.config(pc)
	cfg.Middleware = []fiber.Handler{func(c fiber.Ctx) error {
		c.SetContext(context.WithValue(c.Context(), requestKey{}, c))
		return c.Next()
	}}
	f.load = func(ctx context.Context, _ routecontext.Route) (string, error) {
		c := ctx.Value(requestKey{}).(fiber.Ctx)
		c.Set("Cache-Control", "public, max-age=60")
		c.Set("Content-Language", "en")
		c.Set("X-Session-Trace", "never replay")
		return "original", nil
	}
	app := dualApp(t, cfg)
	first, _, err := dualRequest(app, false, dualPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := dualRequest(app, false, dualPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	nav, _, err := dualRequest(app, true, dualPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Header.Get("Cache-Control") != "public, max-age=60" || second.Header.Get("Content-Language") != "en" || nav.Header.Get("Cache-Control") != "no-store" || nav.Header.Get("Content-Language") != "en" {
		t.Fatal("representation headers lost")
	}
	if second.Header.Get("X-Session-Trace") != "" || nav.Header.Get("X-Session-Trace") != "" {
		t.Fatal("request-specific header replayed")
	}
	if first.Header.Get("X-Request-ID") == "" || second.Header.Get("X-Request-ID") == first.Header.Get("X-Request-ID") {
		t.Fatal("request ID not regenerated")
	}
	if f.loads.Load() != 1 {
		t.Fatal("header test did not hit cache")
	}
}

func TestDualPageCacheDevelopmentBypassesPersistedPairs(t *testing.T) {
	f := newDualFixture()
	store := dualStore(t)
	pc := pagecache.NewPageCache(store, cache.Never, pagecache.PublicRoutes(dualRouteID))
	assertDual(t, dualApp(t, f.config(pc)), false, "original")
	f.value.Store("edited")
	cfg := f.config(pc)
	cfg.DevOrigin = "http://127.0.0.1:5173"
	app := dualApp(t, cfg)
	for _, nav := range []bool{false, true, false} {
		assertDual(t, app, nav, "edited")
	}
	if f.loads.Load() != 4 {
		t.Fatal("development reused persisted output")
	}
}
