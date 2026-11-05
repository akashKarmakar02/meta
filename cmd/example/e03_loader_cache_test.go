package main

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/app/loaders"
	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
)

func TestE03CachedSSRAndCSR(t *testing.T) {
	store, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 64, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var ssr, csr atomic.Int32
	scope := func(context.Context, routecontext.Route) (string, bool, error) { return "test-public-v1", true, nil }
	post := func(_ context.Context, r routecontext.Route) (loaders.PostData, error) {
		ssr.Add(1)
		id, err := r.Params.Int64("postId")
		if err != nil {
			return loaders.PostData{}, err
		}
		return loaders.PostData{ID: id, Title: "title " + r.Query.Get("tab"), Content: "typed content"}, nil
	}
	dashboard := func(_ context.Context, r routecontext.Route) (loaders.DashboardData, error) {
		csr.Add(1)
		id, _ := r.Params.Get("teamId")
		return loaders.DashboardData{TeamID: id, Title: "Team " + id, Greeting: "Cached greeting " + r.Query.Get("tab")}, nil
	}
	reg, err := newLoaderRegistry([]loader.Binding{
		loader.BindCached("posts/[postId]/page.templ", post, loader.CacheOptions[loaders.PostData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: scope, Codec: loader.JSONCodec[loaders.PostData]()}),
		loader.BindCached("dashboard/[teamId]/page.svelte", dashboard, loader.CacheOptions[loaders.DashboardData]{Store: store, Policy: cache.Policy{TTL: time.Minute}, Scope: scope, Codec: loader.JSONCodec[loaders.DashboardData]()}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, reg)
	cases := []struct {
		url  string
		nav  bool
		want string
	}{
		{"/posts/42?tab=one", false, "title one"}, {"/posts/42?tab=one", true, "typed content"},
		{"/posts/99?tab=one", false, "99"}, {"/posts/42?tab=two", false, "title two"},
		{"/dashboard/alpha?tab=one", false, "Cached greeting one"}, {"/dashboard/alpha?tab=one", true, "Cached greeting one"},
		{"/dashboard/beta?tab=one", true, "beta"}, {"/dashboard/alpha?tab=two", true, "Cached greeting two"},
	}
	for i, tc := range cases {
		status, body := getBody(t, app, fmt.Sprint(i), tc.url, tc.nav)
		if status != 200 || !strings.Contains(body, tc.want) {
			t.Fatalf("%s: %d %s", tc.url, status, body)
		}
	}
	if ssr.Load() != 3 || csr.Load() != 3 {
		t.Fatalf("calls SSR=%d CSR=%d", ssr.Load(), csr.Load())
	}
}
