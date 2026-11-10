package pagecache

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/gofiber/fiber/v3"
)

func testPair() *GeneratedPage {
	return &GeneratedPage{HTML: CachedResponse{StatusCode: 200, ContentType: "text/html; charset=utf-8", Body: []byte("<h1>paired</h1>")}, Navigation: CachedResponse{StatusCode: 200, ContentType: "application/json", Body: []byte(`{"version":1,"html":"paired"}`)}}
}

func TestEnvelopeRejectsOldCorruptAndPartialEntries(t *testing.T) {
	encoded, err := encodePage(testPair())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePage(encoded); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)-1] ^= 1
	for _, raw := range [][]byte{nil, []byte("text/html\x00<h1>beta.5</h1>"), []byte("application/json\x00{}"), encoded[:len(encoded)-1], corrupt, append([]byte("META-SSR\x02"), encoded[9:]...)} {
		if _, err := decodePage(raw); err == nil {
			t.Fatalf("accepted invalid envelope: %q", raw)
		}
	}
	partial := testPair()
	partial.Navigation.Body = nil
	if _, err := encodePage(partial); err == nil {
		t.Fatal("accepted incomplete pair")
	}
}

func FuzzDecodePage(f *testing.F) {
	encoded, _ := encodePage(testPair())
	f.Add(encoded)
	f.Add([]byte("text/html\x00legacy"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		page, err := decodePage(raw)
		if err == nil && !validPage(page) {
			t.Fatal("decoder accepted incomplete pair")
		}
	})
}

func TestOldNamespaceCannotPoisonDualCache(t *testing.T) {
	s := newCacheMemory(t)
	old, _ := cache.NewKey("page", "/x")
	if err := s.Set(context.Background(), old, cache.Entry{Value: []byte("text/html\x00OLD")}, cache.Policy{TTL: cache.Never}); err != nil {
		t.Fatal(err)
	}
	pc := NewPageCache(s, cache.Never, PublicRoutes("page.templ"))
	var calls atomic.Int64
	app := fiber.New()
	app.Get("/x", httpserver.WithRouteParams(routing.Route{ID: "page.templ", Path: "/x"}, pc.Wrap(func(c fiber.Ctx) error { t.Error("unexpected uncached path"); return nil }, func(fiber.Ctx) (*GeneratedPage, error) { calls.Add(1); return testPair(), nil })))
	for _, nav := range []bool{true, false} {
		req := httptest.NewRequest("GET", "/x", nil)
		if nav {
			req.Header.Set(httpserver.NavigationHeader, "1")
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("generations: %d", calls.Load())
	}
}

func TestWrapWithoutGeneratorPassesThrough(t *testing.T) {
	pc := NewPageCache(newCacheMemory(t), cache.Never, PublicRoutes("page.templ"))
	var calls atomic.Int64
	app := fiber.New()
	app.Get("/x", pc.Wrap(func(c fiber.Ctx) error { calls.Add(1); return c.SendString("uncached") }))
	for range 2 {
		res, err := app.Test(httptest.NewRequest("GET", "/x", nil))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if calls.Load() != 2 {
		t.Fatal("single-response handlers cannot populate dual cache")
	}
}

func TestGenerationTimeoutAndWaiterCancellation(t *testing.T) {
	pc := NewPageCache(newCacheMemory(t), cache.Never, PublicRoutes("page.templ"), GenerationTimeout(20*time.Millisecond))
	app := fiber.New()
	started := make(chan struct{})
	var calls atomic.Int64
	var cancelled atomic.Bool
	app.Get("/x", httpserver.WithRouteParams(routing.Route{ID: "page.templ", Path: "/x"}, pc.Wrap(func(fiber.Ctx) error { return errors.New("unexpected fallback") }, func(c fiber.Ctx) (*GeneratedPage, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-c.Context().Done()
		cancelled.Store(true)
		return nil, c.Context().Err()
	})))
	res, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 500 || !cancelled.Load() {
		t.Fatal("generation did not honor bound")
	}
	pc.mu.Lock()
	active := len(pc.flights)
	pc.mu.Unlock()
	if active != 0 {
		t.Fatal("timed-out generation leaked its flight")
	}
	// Test a waiter with its own standard context, without fabricating Fiber
	// request objects outside the normal handler lifetime.
	pc2 := NewPageCache(newCacheMemory(t), cache.Never, PublicRoutes("page.templ"))
	app2 := fiber.New()
	ownerStarted, release := make(chan struct{}), make(chan struct{})
	app2.Use(func(c fiber.Ctx) error {
		if c.Get("X-Cancel-Waiter") == "yes" {
			ctx, cancel := context.WithTimeout(c.Context(), 20*time.Millisecond)
			defer cancel()
			c.SetContext(ctx)
		}
		return c.Next()
	})
	app2.Get("/x", httpserver.WithRouteParams(routing.Route{ID: "page.templ", Path: "/x"}, pc2.Wrap(func(fiber.Ctx) error { return errors.New("unexpected fallback") }, func(c fiber.Ctx) (*GeneratedPage, error) {
		close(ownerStarted)
		select {
		case <-release:
			return testPair(), nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	})))
	done := make(chan error, 1)
	go func() {
		res, err := app2.Test(httptest.NewRequest("GET", "/x", nil))
		if err == nil {
			res.Body.Close()
		}
		done <- err
	}()
	<-ownerStarted
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Cancel-Waiter", "yes")
	res, err = app2.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 500 {
		t.Fatal("canceled waiter did not exit")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	res, err = app2.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("waiter canceled the owner")
	}
}

type failWrites struct {
	cache.Store
	fail atomic.Bool
}

func (s *failWrites) Set(ctx context.Context, key cache.Key, e cache.Entry, p cache.Policy) error {
	if s.fail.Load() {
		return errors.New("write unavailable")
	}
	return s.Store.Set(ctx, key, e, p)
}

func TestFailedInvalidationDoesNotClaimSuccess(t *testing.T) {
	s := &failWrites{Store: newCacheMemory(t)}
	pc := NewPageCache(s, cache.Never)
	if err := pc.InvalidatePath(context.Background(), "/x"); err != nil {
		t.Fatal(err)
	}
	s.fail.Store(true)
	if err := pc.InvalidatePath(context.Background(), "/x"); err == nil {
		t.Fatal("invalidation failure hidden")
	}
	if err := pc.InvalidatePath(nil, "/x"); !errors.Is(err, cache.ErrNilContext) && err == nil {
		t.Fatal("nil context accepted")
	}
}

type failPageWrites struct{ cache.Store }

func (s failPageWrites) Set(ctx context.Context, key cache.Key, entry cache.Entry, policy cache.Policy) error {
	if _, err := decodePage(entry.Value); err == nil {
		return errors.New("page write unavailable")
	}
	return s.Store.Set(ctx, key, entry, policy)
}

func TestPageWriteFailureDoesNotFailOrPoisonResponse(t *testing.T) {
	pc := NewPageCache(failPageWrites{newCacheMemory(t)}, cache.Never, PublicRoutes("page.templ"))
	var generations atomic.Int64
	app := fiber.New()
	app.Get("/x", httpserver.WithRouteParams(routing.Route{ID: "page.templ", Path: "/x"}, pc.Wrap(func(fiber.Ctx) error { return errors.New("unexpected fallback") }, func(fiber.Ctx) (*GeneratedPage, error) { generations.Add(1); return testPair(), nil })))
	for range 2 {
		res, err := app.Test(httptest.NewRequest("GET", "/x", nil))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("cache write error failed successful rendering")
		}
	}
	if generations.Load() != 2 {
		t.Fatal("failed write became a poisoned hit")
	}
}

func TestMissingPathTokenCannotResurrectUnreachablePage(t *testing.T) {
	s := newCacheMemory(t)
	pc := NewPageCache(s, cache.Never, PublicRoutes("page.templ"))
	var generations atomic.Int64
	app := fiber.New()
	app.Get("/x", httpserver.WithRouteParams(routing.Route{ID: "page.templ", Path: "/x"}, pc.Wrap(func(fiber.Ctx) error { return errors.New("unexpected fallback") }, func(fiber.Ctx) (*GeneratedPage, error) { generations.Add(1); return testPair(), nil })))
	for i := range 2 {
		res, err := app.Test(httptest.NewRequest("GET", "/x", nil))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if i == 0 {
			if err := s.Delete(context.Background(), pc.tokenKey("/x")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if generations.Load() != 2 {
		t.Fatal("missing token reused old generation")
	}
}

func TestMiddlewareCannotMutateSharedGenerationBytes(t *testing.T) {
	pc := NewPageCache(newCacheMemory(t), cache.Never, PublicRoutes("page.templ"))
	pair := testPair()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		err := c.Next()
		if body := c.Response().Body(); len(body) > 0 {
			body[0] = '!'
		}
		return err
	})
	app.Get("/x", httpserver.WithRouteParams(routing.Route{ID: "page.templ", Path: "/x"}, pc.Wrap(func(fiber.Ctx) error { return errors.New("unexpected fallback") }, func(fiber.Ctx) (*GeneratedPage, error) { return pair, nil })))
	res, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if pair.HTML.Body[0] != '<' {
		t.Fatal("response middleware modified immutable bytes shared with flight waiters")
	}
}
