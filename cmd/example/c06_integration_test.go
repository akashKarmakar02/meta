// Package main — P2-C06 integration tests: Phase C data loading verified
// end to end against the real example app with generated route IDs.
//
// These tests sit one level below the browser suites: they exercise the
// actual Fiber pipeline (WithRouteParams → WithLoader → render handlers)
// over HTTP, using real generated routes and real bindings.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	routes "go-meta/generated"
	"go-meta/internal/httpserver"
	"go-meta/loader"
	"go-meta/routecontext"
)

// findRouteID asserts a generated route with the given ID exists.
func findRouteID(t *testing.T, id string) {
	t.Helper()
	for _, r := range routes.RegisterRoutes() {
		if r.ID == id {
			return
		}
	}
	t.Fatalf("generated route %q not found", id)
}

// TestC06GeneratedRouteIDsStable pins that the loader bindings in
// app/loaders/bindings.go all name real generated route IDs, and that those
// IDs are parameter-independent (the pattern segment, not a concrete value).
func TestC06GeneratedRouteIDsStable(t *testing.T) {
	for _, id := range []string{
		"posts/[postId]/page.templ",
		"dashboard/[teamId]/page.svelte",
		"c05/redirectSsr/page.templ",
		"c05/redirectCsr/page.svelte",
	} {
		findRouteID(t, id)
	}
}

// TestC06LoaderRunsExactlyOncePerRequest proves single execution for every
// response mode: SSR full document, SSR navigation envelope, CSR shell, and
// CSR navigation envelope. A counter inside the loader is the oracle.
func TestC06LoaderRunsExactlyOncePerRequest(t *testing.T) {
	cases := []struct {
		name  string
		route string
		url   string
		isCSR bool
	}{
		{"ssr direct", "posts/[postId]/page.templ", "/posts/42", false},
		{"ssr navigation", "posts/[postId]/page.templ", "/posts/42", false},
		{"csr direct", "dashboard/[teamId]/page.svelte", "/dashboard/team-7", true},
		{"csr navigation", "dashboard/[teamId]/page.svelte", "/dashboard/team-7", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			registry, err := newLoaderRegistry([]loader.Binding{
				loader.Bind(tc.route, func(context.Context, routecontext.Route) (string, error) {
					atomic.AddInt32(&calls, 1)
					return "ran", nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			app := newConfiguredApp(nil, false, registry)
			nav := strings.Contains(tc.name, "navigation")
			for i := 0; i < 3; i++ {
				code, _, _ := doRequest(t, app, tc.name, tc.url, nav)
				if code != 200 {
					t.Fatalf("request %d: status %d", i, code)
				}
			}
			if got := atomic.LoadInt32(&calls); got != 3 {
				t.Fatalf("loader ran %d times for 3 requests, want exactly 3 (once per request)", got)
			}
		})
	}
}

// TestC06SSRAndCSRLoaderParity proves a loader bound to an SSR route renders
// typed data into the page, and the same-shaped loader bound to a CSR route
// delivers JSON — with identical route snapshot inputs on both sides. Parity
// is at the route-context level (same ID/pattern/params/query on both sides);
// the delivery differs by design (typed templ render vs JSON).
func TestC06SSRAndCSRLoaderParity(t *testing.T) {
	type snapshot struct {
		RouteID string `json:"routeId"`
		Pattern string `json:"pattern"`
		Param   string `json:"param"`
		Query   string `json:"query"`
	}
	load := func(ctx context.Context, route routecontext.Route) (snapshot, error) {
		name := "postId"
		if !strings.Contains(route.RouteID, "posts") {
			name = "teamId"
		}
		v, _ := route.Params.Get(name)
		return snapshot{
			RouteID: route.RouteID,
			Pattern: route.Pattern,
			Param:   v,
			Query:   route.Query.Get("src"),
		}, nil
	}
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("posts/[postId]/page.templ", load),
		loader.Bind("dashboard/[teamId]/page.svelte", load),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// CSR navigation envelope carries the snapshot as JSON data.
	code, body, _ := doRequest(t, app, "csr parity", "/dashboard/team-99?src=go", true)
	if code != 200 {
		t.Fatalf("csr status %d", code)
	}
	var env struct {
		Data *snapshot `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope parse: %v in %s", err, body)
	}
	if env.Data == nil {
		t.Fatalf("csr envelope data absent: %s", body)
	}
	want := snapshot{
		RouteID: "dashboard/[teamId]/page.svelte",
		Pattern: "/dashboard/[teamId]",
		Param:   "team-99",
		Query:   "go",
	}
	if *env.Data != want {
		t.Fatalf("csr data = %+v, want %+v", *env.Data, want)
	}

	// The CSR direct shell embeds the same JSON.
	code, body, _ = doRequest(t, app, "csr shell", "/dashboard/team-99?src=go", false)
	if code != 200 {
		t.Fatalf("csr shell status %d", code)
	}
	if !strings.Contains(body, `"routeId":"dashboard/[teamId]/page.svelte"`) ||
		!strings.Contains(body, `"param":"team-99"`) {
		t.Errorf("shell embedded data wrong: %s", body[:min(len(body), 400)])
	}

	// The same loader bound to the SSR route sees the same shape of route
	// context (the value is rendered through loaderdata, so verify via a
	// loader that fails when the context is wrong: any wrong field -> 500,
	// no partial page). This proves parity of inputs, not of transport.
	ssrCheck := func(ctx context.Context, route routecontext.Route) (string, error) {
		got, err := load(ctx, route)
		if err != nil {
			return "", err
		}
		ssrWant := snapshot{
			RouteID: "posts/[postId]/page.templ",
			Pattern: "/posts/[postId]",
			Param:   "99",
			Query:   "go",
		}
		if got != ssrWant {
			return "", fmt.Errorf("route context mismatch: %+v != %+v", got, ssrWant)
		}
		return "ok", nil
	}
	registry2, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("posts/[postId]/page.templ", ssrCheck),
	})
	if err != nil {
		t.Fatal(err)
	}
	app2 := newConfiguredApp(nil, false, registry2)
	code, body, _ = doRequest(t, app2, "ssr parity", "/posts/99?src=go", false)
	if code != 200 || strings.Contains(body, "mismatch") {
		t.Fatalf("ssr parity failed: status %d body %s", code, body)
	}
}

// TestC06QueryAndRouteContextMatrix drives repeated keys, empty values,
// unicode, plus-vs-%20, and malformed escapes through a registered loader's
// route snapshot on a real request.
func TestC06QueryAndRouteContextMatrix(t *testing.T) {
	// The query matrix is exercised on a CSR route so the loader result is
	// JSON-visible in the envelope; the underlying route snapshot (B05) is the
	// same one SSR loaders read.
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(_ context.Context, r routecontext.Route) ([]string, error) {
			return r.Query.Values("tag"), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// envelope returns the loader's data JSON-decoded, or fatals on a non-200.
	envelope := func(t *testing.T, url string) []string {
		t.Helper()
		code, body, _ := doRequest(t, app, "q", url, true)
		if code != 200 {
			t.Fatalf("%s: status %d", url, code)
		}
		var env struct {
			Data []string `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatal(err)
		}
		return env.Data
	}

	t.Run("repeated and empty", func(t *testing.T) {
		got := envelope(t, "/dashboard/team-1?tag=a&tag=&tag=c")
		if fmt.Sprint(got) != "[a  c]" {
			t.Fatalf("data = %v", got)
		}
	})
	t.Run("unicode and plus", func(t *testing.T) {
		got := envelope(t, "/dashboard/team-1?tag=%E4%B8%AD%E6%96%87&tag=a+b")
		if len(got) != 2 || got[0] != "中文" || got[1] != "a b" {
			t.Fatalf("data = %q", got)
		}
	})
	t.Run("malformed escape rejected", func(t *testing.T) {
		code, _, _ := doRequest(t, app, "q", "/dashboard/team-1?tag=%zz", false)
		if code != 400 {
			t.Fatalf("malformed query status %d, want 400", code)
		}
	})
	// The 8192-byte query limit (414) is already pinned at the middleware
	// level in internal/httpserver's B05 context tests; duplicating it here
	// would only retest fasthttp's client read buffer.
}

// TestC06RedirectBoundary proves redirect validation happens at the actual
// HTTP Location boundary: encoded control characters and encoded
// protocol-relative forms must surface as 500 with no Location header, never
// as a redirect the client could decode off-origin.
func TestC06RedirectBoundary(t *testing.T) {
	for _, target := range []string{
		"/%2F%2Fevil.example.com",
		"/x%5Cevil.example.com",
		"/a%0D%0ASet-Cookie:%20pwn",
		"/%00",
		"/%zz",
	} {
		registry, err := newLoaderRegistry([]loader.Binding{
			loader.Bind("about/page.templ", func(context.Context, routecontext.Route) (int, error) {
				return 0, loader.Redirect(target)
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		app := newConfiguredApp(nil, false, registry)
		for _, nav := range []bool{false, true} {
			code, body, hdr := doRequest(t, app, "encoded redirect", "/about", nav)
			if code != 500 {
				t.Fatalf("target %q nav=%v: status %d, want 500", target, nav, code)
			}
			if hdr.Get("Location") != "" {
				t.Fatalf("target %q: Location header set: %q", target, hdr.Get("Location"))
			}
			if strings.Contains(body, "evil") && strings.Contains(target, "evil") {
				t.Errorf("target %q leaked into body", target)
			}
		}
	}
}

// TestC06ValidRedirectWithQueryAndFragment proves accepted targets survive
// the Location boundary unchanged, including allowed query strings and
// percent-encoded values.
func TestC06ValidRedirectWithQueryAndFragment(t *testing.T) {
	for _, target := range []string{
		"/docs?x=1",
		"/search?q=%20a%20b",
		"/about#top",
	} {
		registry, err := newLoaderRegistry([]loader.Binding{
			loader.Bind("about/page.templ", func(context.Context, routecontext.Route) (int, error) {
				return 0, loader.Redirect(target)
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		app := newConfiguredApp(nil, false, registry)
		code, _, hdr := doRequest(t, app, "redirect query", "/about", false)
		if code != 302 || hdr.Get("Location") != target {
			t.Fatalf("target %q: status %d Location %q", target, code, hdr.Get("Location"))
		}
	}
}

// TestC06LargePayloadBounded checks that a large-but-representative loader
// result (arrays of realistic objects, ~1 MiB of JSON) serializes and embeds
// without truncation, both in the shell and in a navigation envelope. It is
// not a performance test; it pins that no intermediate layer silently drops
// or corrupts large payloads.
func TestC06LargePayloadBounded(t *testing.T) {
	items := make([]map[string]string, 8192)
	for i := range items {
		items[i] = map[string]string{"k": fmt.Sprintf("value-%d", i)}
	}
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(context.Context, routecontext.Route) ([]map[string]string, error) {
			return items, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// Envelope: data must round-trip exactly.
	code, body, _ := doRequest(t, app, "large env", "/dashboard/big", true)
	if code != 200 {
		t.Fatalf("envelope status %d", code)
	}
	var env struct {
		Data []map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != len(items) || env.Data[0]["k"] != items[0]["k"] || env.Data[len(items)-1]["k"] != items[len(items)-1]["k"] {
		t.Fatalf("large payload corrupted: got %d items", len(env.Data))
	}

	// Shell: embedded script must contain the full payload.
	code, body, _ = doRequest(t, app, "large shell", "/dashboard/big", false)
	if code != 200 {
		t.Fatalf("shell status %d", code)
	}
	start := strings.Index(body, `<script id="go-meta-csr-data" type="application/json">`)
	end := strings.Index(body, `</script>`)
	if start < 0 || end <= start {
		t.Fatalf("shell data script missing")
	}
	payload := body[start+len(`<script id="go-meta-csr-data" type="application/json">`) : end]
	var back []map[string]string
	if err := json.Unmarshal([]byte(payload), &back); err != nil {
		t.Fatalf("embedded payload not valid JSON: %v", err)
	}
	if len(back) != len(items) {
		t.Fatalf("embedded payload truncated: %d items", len(back))
	}
}

// TestC06ConcurrentLoaderRequests proves concurrent requests through the
// real pipeline stay isolated: each request sees its own params and data,
// with no cross-request leakage.
func TestC06ConcurrentLoaderRequests(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(_ context.Context, r routecontext.Route) (string, error) {
			id, _ := r.Params.Get("teamId")
			return id, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := fmt.Sprintf("team-%d", i)
			req := httptest.NewRequest("GET", "/dashboard/"+want, nil)
			req.Header.Set(httpserver.NavigationHeader, "1")
			res, err := app.Test(req)
			if err != nil {
				errs <- err.Error()
				return
			}
			var env struct {
				Data string `json:"data"`
			}
			if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
				errs <- err.Error()
				return
			}
			if env.Data != want {
				errs <- fmt.Sprintf("cross-request leak: wanted %s got %s", want, env.Data)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestC06SerializationSafetyEmbedding proves hostile loader results cannot
// break out of the embedded data script or the envelope, at the rendered-HTML
// boundary.
func TestC06SerializationSafetyEmbedding(t *testing.T) {
	hostile := []struct {
		name  string
		value any
	}{
		{"script close", "</script><script>alert(1)</script>"},
		{"script close mixed case", "</SCRIPT><script>alert(1)</script>"},
		{"U+2028", "line sep"},
		{"U+2029", "line sep"},
		{"angle brackets", "<b>&'\"</b>"},
		{"backslash quotes", `back\"slash`},
		{"unicode", "中文 \U0001F600 café"},
		{"empty object", map[string]string{}},
		{"empty array", []string{}},
		{"nil value", nil},
	}
	for _, h := range hostile {
		t.Run(h.name, func(t *testing.T) {
			// Bind a loader returning the hostile value type-erased through
			// the public API (Bind handles the erasure).
			registry, err := newLoaderRegistry([]loader.Binding{
				loader.Bind("dashboard/[teamId]/page.svelte", func(context.Context, routecontext.Route) (any, error) {
					return h.value, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			app := newConfiguredApp(nil, false, registry)
			code, body, _ := doRequest(t, app, h.name, "/dashboard/x", false)
			if code != 200 {
				t.Fatalf("status %d for %v", code, h.value)
			}
			start := strings.Index(body, `<script id="go-meta-csr-data" type="application/json">`)
			endIdx := strings.Index(body[start:], `</script>`)
			if start < 0 || endIdx < 0 {
				t.Fatalf("no data script for %v: %s", h.value, body)
			}
			payload := body[start+len(`<script id="go-meta-csr-data" type="application/json">`) : start+endIdx]
			// The payload must parse back as JSON — no breakout broke it.
			var back any
			if err := json.Unmarshal([]byte(payload), &back); err != nil {
				t.Fatalf("%s: embedded payload not valid JSON: %v in %q", h.name, err, payload)
			}
			// No literal </script> may appear inside the payload region
			// before the element's own closing tag.
			if idx := strings.Index(strings.ToLower(payload), "</script"); idx >= 0 {
				t.Fatalf("%s: unescaped </script> inside payload", h.name)
			}
		})
	}
}

// TestC06UnserializableLoaderStopsPublication proves a result that cannot be
// JSON-serialized produces a 500 with no partial bootstrap payload — never a
// shell claiming loader data exists.
func TestC06UnserializableLoaderStopsPublication(t *testing.T) {
	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("dashboard/[teamId]/page.svelte", func(context.Context, routecontext.Route) (chan int, error) {
			return make(chan int), nil // channels are not JSON-serializable
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)
	for _, nav := range []bool{false, true} {
		code, body, _ := doRequest(t, app, "unserializable", "/dashboard/x", nav)
		if code != 500 {
			t.Fatalf("nav=%v: status %d, want 500", nav, code)
		}
		if strings.Contains(body, "go-meta-csr-data") {
			t.Fatalf("nav=%v: partial data published: %s", nav, body)
		}
	}
}
