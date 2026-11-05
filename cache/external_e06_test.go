package cache_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestPhaseEPublicAPIFromAnotherModule (E06 §44): an isolated external Go
// module exercises the full public Phase E surface — cache.Store, NewMemory,
// NewKey, loader.BindCached, LoaderCacheKey, NewInvalidator, NewFlightGroup —
// through store creation, cached binding, miss/hit, invalidation, refill and
// singleflight, without importing any internal/ package.
func TestPhaseEPublicAPIFromAnotherModule(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	module := "module example.test/consumer\n\ngo 1.26.10\n\nrequire github.com/akashKarmakar02/meta v0.0.0\nreplace github.com/akashKarmakar02/meta => " + strconv.Quote(root) + "\n"
	source := `package consumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/cache"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
)

type post struct {
	Title string ` + "`json:\"title\"`" + `
	Tags  []string ` + "`json:\"tags\"`" + `
}

func TestExternalPhaseE(t *testing.T) {
	// Store creation with every public constructor.
	store, err := cache.NewKey("ns", "a:b", "c")
	if err != nil || store == "" {
		t.Fatalf("NewKey: %v", err)
	}
	mem, err := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16, MaxBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	var s cache.Store = mem // Store is externally implementable/usable

	inv, err := loader.NewInvalidator(s)
	if err != nil {
		t.Fatal(err)
	}
	group, err := loader.NewFlightGroup(inv, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	load := func(context.Context, routecontext.Route) (post, error) {
		calls++
		return post{Title: "hello", Tags: []string{"a"}}, nil
	}
	opts := loader.CacheOptions[post]{
		Store:       s,
		Policy:      cache.Policy{TTL: time.Minute},
		Scope:       func(context.Context, routecontext.Route) (string, bool, error) { return "public", true, nil },
		Codec:       loader.JSONCodec[post](),
		Invalidator: inv,
		Flights:     group,
	}
	binding := loader.BindCached("posts/[id]/page.templ", load, opts)
	registry, err := loader.NewRegistry([]string{"posts/[id]/page.templ"}, binding)
	if err != nil {
		t.Fatal(err)
	}
	route := routecontext.Route{RouteID: "posts/[id]/page.templ"}
	route.Params = routeparams.NewParams(map[string]string{"id": "42"})

	// Cache miss then hit.
	exec, ok := registry.Lookup("posts/[id]/page.templ")
	if !ok {
		t.Fatal("lookup failed")
	}
	first, err := exec.Execute(context.Background(), route)
	if err != nil {
		t.Fatal(err)
	}
	second, err := exec.Execute(context.Background(), route)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("miss/hit executed %d loads", calls)
	}
	p := second.(post)
	if p.Title != first.(post).Title || len(p.Tags) != 1 {
		t.Fatalf("hit lost typed data: %#v", p)
	}

	// Invalidation through the public key builder.
	key, err := loader.LoaderCacheKey(route, "public")
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.InvalidateKey(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	third, err := exec.Execute(context.Background(), route)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("invalidation did not force a refill: %d loads", calls)
	}
	if third.(post).Title != "hello" {
		t.Fatal("refill lost data")
	}

	// Singleflight over a fresh key: two concurrent misses share one load.
	route.Params = routeparams.NewParams(map[string]string{"id": "sf"})
	done := make(chan struct{})
	sync := 0
	load2 := func(context.Context, routecontext.Route) (post, error) {
		sync++
		if sync == 1 {
			<-done
		}
		return post{Title: "shared"}, nil
	}
	opts2 := opts
	binding2 := loader.BindCached("posts/[id]/page.templ", load2, opts2)
	registry2, err := loader.NewRegistry([]string{"posts/[id]/page.templ"}, binding2)
	if err != nil {
		t.Fatal(err)
	}
	exec2, _ := registry2.Lookup("posts/[id]/page.templ")
	results := make(chan error, 2)
	go func() { _, err := exec2.Execute(context.Background(), route); results <- err }()
	go func() { _, err := exec2.Execute(context.Background(), route); results <- err }()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	close(done)
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "consumer.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external consumer: %v\n%s", err, output)
	}
}
