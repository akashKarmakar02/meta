package loader

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
)

func sortStrings(s []string) { sort.Strings(s) }

// E06 §13: the cache-key identity matrix, driven through real cached
// bindings (actual lookups and returned results, not string comparisons).
// Each case must produce its own cache entry: exactly one load for a
// repeated visit and distinct loads across identities.
func TestE06CacheKeyIdentityMatrix(t *testing.T) {
	// The route fixtures mirror what httpserver.WithRouteParams builds:
	// named params are decoded segments; a catch-all is stored under the
	// route's declared catch-all name as the slash-joined decoded value
	// (e.g. "getting-started/installation").
	catchAll := func(segments ...string) routeparams.Params {
		joined := ""
		for i, s := range segments {
			if i > 0 {
				joined += "/"
			}
			joined += s
		}
		return routeparams.NewParams(map[string]string{"path": joined})
	}
	cases := []struct {
		name   string
		routeA routecontext.Route
		routeB routecontext.Route
	}{
		{
			name:   "different dynamic parameters",
			routeA: route("demo/[id]/page.templ", "7", nil),
			routeB: route("demo/[id]/page.templ", "8", nil),
		},
		{
			name: "nested dynamic parameters differ in one segment",
			routeA: routecontext.Route{RouteID: "docs/[section]/[topic]/page.templ",
				Params: routeparams.NewParams(map[string]string{"section": "intro", "topic": "a"})},
			routeB: routecontext.Route{RouteID: "docs/[section]/[topic]/page.templ",
				Params: routeparams.NewParams(map[string]string{"section": "intro", "topic": "b"})},
		},
		{
			name: "nested dynamic parameter names collide by value",
			routeA: routecontext.Route{RouteID: "docs/[a]/[b]/page.templ",
				Params: routeparams.NewParams(map[string]string{"a": "x", "b": "y"})},
			routeB: routecontext.Route{RouteID: "docs/[a]/[b]/page.templ",
				Params: routeparams.NewParams(map[string]string{"a": "x/y", "b": ""})},
		},
		{
			name:   "catch-all depth differs",
			routeA: routecontext.Route{RouteID: "wiki/+/page.templ", Params: catchAll("one")},
			routeB: routecontext.Route{RouteID: "wiki/+/page.templ", Params: catchAll("one", "two")},
		},
		{
			name:   "catch-all segment values differ",
			routeA: routecontext.Route{RouteID: "wiki/+/page.templ", Params: catchAll("a", "b")},
			routeB: routecontext.Route{RouteID: "wiki/+/page.templ", Params: catchAll("a-b")},
		},
		{
			name:   "query value differs",
			routeA: route("r", "1", url.Values{"tab": {"one"}}),
			routeB: route("r", "1", url.Values{"tab": {"two"}}),
		},
		{
			name:   "repeated query order differs",
			routeA: route("r", "1", url.Values{"tag": {"x", "y"}}),
			routeB: route("r", "1", url.Values{"tag": {"y", "x"}}),
		},
		{
			name:   "missing versus empty query value",
			routeA: route("r", "1", url.Values{"q": {""}}),
			routeB: route("r", "1", nil),
		},
		{
			name:   "empty versus one-empty-element repeated query",
			routeA: route("r", "1", url.Values{"q": {""}}),
			routeB: route("r", "1", url.Values{"q": {"", ""}}),
		},
		{
			name:   "unicode parameters",
			routeA: route("r", "ünïcode/雪", nil),
			routeB: route("r", "ünïcode/雪雪", nil),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memory(t)
			var loads atomic.Int32
			load := func(_ context.Context, r routecontext.Route) (testData, error) {
				loads.Add(1)
				parts := make([]string, 0, len(r.Params.All())+2)
				for k, v := range r.Params.All() {
					parts = append(parts, k+"="+v)
				}
				sortStrings(parts)
				for k, vs := range r.Query.All() {
					for _, v := range vs {
						parts = append(parts, "?"+k+"="+v)
					}
				}
				sortStrings(parts[len(r.Params.All()):])
				return testData{Name: strings.Join(parts, "&")}, nil
			}
			opts := CacheOptions[testData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: publicScope, Codec: JSONCodec[testData]()}
			h := registered(t, tc.routeA.RouteID, load, opts)
			ctx := context.Background()
			// Each identity caches independently: first visit fills, second hits.
			a1, err := h.Execute(ctx, tc.routeA)
			if err != nil {
				t.Fatal(err)
			}
			a2, err := h.Execute(ctx, tc.routeA)
			if err != nil {
				t.Fatal(err)
			}
			if a1.(testData).Name != a2.(testData).Name {
				t.Fatalf("repeat visit changed data: %q -> %q", a1, a2)
			}
			b1, err := h.Execute(ctx, tc.routeB)
			if err != nil {
				t.Fatal(err)
			}
			if got := loads.Load(); got != 2 {
				t.Fatalf("expected exactly 2 loads (one per identity), got %d — identities collided", got)
			}
			// The identities are distinct keys: different data OR different key
			// bytes must result. (Different data with equal keys is impossible:
			// one entry would win and the second load would not happen.)
			if b1.(testData).Name == a1.(testData).Name {
				ka, err := LoaderCacheKey(tc.routeA, "public-v1")
				if err != nil {
					t.Fatal(err)
				}
				kb, err := LoaderCacheKey(tc.routeB, "public-v1")
				if err != nil {
					t.Fatal(err)
				}
				if ka == kb {
					t.Fatalf("distinct identities share one cache key: %q", ka)
				}
			}
		})
	}
}

// TestE06CacheKeyScopeSeparationMatrix pins that scope participates in key
// identity even for otherwise identical routes: different tenant scopes,
// different authenticated-user scopes and an explicit bypass each behave
// as their own identity.
func TestE06CacheKeyScopeSeparationMatrix(t *testing.T) {
	store := memory(t)
	type principalKey struct{}
	var loads atomic.Int32
	scope := func(ctx context.Context, _ routecontext.Route) (string, bool, error) {
		p, _ := ctx.Value(principalKey{}).(string)
		if p == "anon" {
			return "", false, nil // explicit bypass
		}
		return p, p != "", nil
	}
	load := func(ctx context.Context, _ routecontext.Route) (testData, error) {
		loads.Add(1)
		p, _ := ctx.Value(principalKey{}).(string)
		return testData{Name: "data-for-" + p}, nil
	}
	h := registered(t, "secure/[id]/page.templ", load, CacheOptions[testData]{
		Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: scope, Codec: JSONCodec[testData]()})
	ctxFor := func(p string) context.Context {
		return context.WithValue(context.Background(), principalKey{}, p)
	}
	r := route("secure/[id]/page.templ", "42", nil)
	// Tenant scopes are isolated entries.
	for _, p := range []string{"tenant-a", "tenant-b", "user-1", "user-2"} {
		got, err := h.Execute(ctxFor(p), r)
		if err != nil {
			t.Fatal(err)
		}
		if got.(testData).Name != "data-for-"+p {
			t.Fatalf("scope %s received %q", p, got.(testData).Name)
		}
	}
	// 4 distinct principals => 4 fills.
	if got := loads.Load(); got != 4 {
		t.Fatalf("principal scopes collided: %d loads", got)
	}
	// Repeating a principal is a hit.
	if _, err := h.Execute(ctxFor("tenant-a"), r); err != nil {
		t.Fatal(err)
	}
	if got := loads.Load(); got != 4 {
		t.Fatalf("repeat principal reloaded: %d loads", got)
	}
	// An explicit bypass never reads or writes the store: every bypass
	// executes and no cache entry is created for it.
	before := loads.Load()
	for range 3 {
		got, err := h.Execute(ctxFor("anon"), r)
		if err != nil {
			t.Fatal(err)
		}
		if got.(testData).Name != "data-for-anon" {
			t.Fatalf("bypass received %q", got.(testData).Name)
		}
	}
	if got := loads.Load(); got != before+3 {
		t.Fatalf("bypass did not execute every request: %d -> %d", before, loads.Load())
	}
	// A bypass never falls back to a global key: tenant-a's entry still hits.
	if _, err := h.Execute(ctxFor("tenant-a"), r); err != nil {
		t.Fatal(err)
	}
	if got := loads.Load(); got != before+3 {
		t.Fatal("bypass polluted another scope's cache identity")
	}
}

// TestE06LoaderCacheKeyNamespaceCollision pins that the E03 key builder's
// output stays within NewKey's collision-resistant encoding: route IDs and
// scopes containing separator-like bytes can never collide, and the
// builder rejects empty route IDs (fail-safe, no loader execution).
func TestE06LoaderCacheKeyNamespaceCollision(t *testing.T) {
	r := func(routeID, id string) routecontext.Route {
		return route(routeID, id, nil)
	}
	k1, err := LoaderCacheKey(r("a:b/page.templ", "c"), "s")
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoaderCacheKey(r("a", "b/page.templ"), "c:s")
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Fatal("length-prefixed components collided")
	}
	// Component ambiguity across params and query:
	p1 := routecontext.Route{RouteID: "x", Params: routeparams.NewParams(map[string]string{"a": "1", "b": "2"})}
	p2 := routecontext.Route{RouteID: "x", Params: routeparams.NewParams(map[string]string{"a": "1:2"})}
	kp1, err := LoaderCacheKey(p1, "s")
	if err != nil {
		t.Fatal(err)
	}
	kp2, err := LoaderCacheKey(p2, "s")
	if err != nil {
		t.Fatal(err)
	}
	if kp1 == kp2 {
		t.Fatal("component boundaries are ambiguous")
	}
	// Control characters are rejected by the underlying key validation
	// (deterministic fail-safe rather than silent encoding).
	if _, err := LoaderCacheKey(routecontext.Route{RouteID: "x\ny"}, "s"); err == nil {
		t.Fatal("control character in route ID accepted")
	}
	if _, err := LoaderCacheKey(routecontext.Route{}, "s"); err == nil {
		t.Fatal("empty route ID must be rejected")
	}
	if _, err := LoaderCacheKey(routecontext.Route{RouteID: "r"}, ""); err == nil {
		t.Fatal("empty scope must be rejected")
	}
}
