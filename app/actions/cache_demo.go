// E04 demo actions: an explicit mutation that commits first and invalidates
// the exact cached loader key afterwards. This is the pattern applications
// follow — the framework never guesses which loaders a mutation affects.
package actions

import (
	"context"
	"fmt"

	"github.com/akashKarmakar02/meta/app/loaders"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
)

// DemoInvalidator is set by main before registry construction; the update
// action needs it to invalidate cached loader entries. Package-level state is
// acceptable here only because the example app wires exactly one cache.
var DemoInvalidator *loader.Invalidator

type UpdateCacheDemoInput struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type UpdateCacheDemoResult struct {
	Invalidated bool `json:"invalidated"`
}

// UpdateCacheDemo commits a mutation (version bump on the demo data), then
// invalidates the exact cached entries for both cache-demo routes that can
// show this id. Commit happens strictly before invalidation; if the commit
// fails nothing is invalidated. If invalidation fails after the commit, the
// error is returned — the mutation stays committed (the framework does not
// roll it back); the stale entry survives until its TTL expires or the call
// is retried (it is safe to retry).
func UpdateCacheDemo(_ context.Context, route routecontext.Route, in UpdateCacheDemoInput) (UpdateCacheDemoResult, error) {
	Calls.UpdateCacheDemo.Add(1)
	if in.ID == "" {
		return UpdateCacheDemoResult{}, fmt.Errorf("action: invalid input")
	}
	// 1. Commit the mutation.
	loaders.SetDemoVersion(in.ID, in.Title)
	// 2. Invalidate every cache identity the mutation can affect: both
	//    routes render this id (any query combination was cached under its
	//    own key — only the exact key is invalidated here; broader invalidation
	//    belongs to E04's explicit multiple-key use, shown below for the
	//    tabless variant).
	ctx := context.Background()
	for _, routeID := range []string{
		"cache-demo/post/[id]/page.templ",
		"cache-demo/team/[id]/page.svelte",
	} {
		r := routecontext.Route{RouteID: routeID}
		r.Params = routeparams.NewParams(map[string]string{"id": in.ID})
		key, err := loader.LoaderCacheKey(r, "go-meta-example-public-v1")
		if err != nil {
			return UpdateCacheDemoResult{}, fmt.Errorf("cache key: %w", err)
		}
		if err := DemoInvalidator.InvalidateKey(ctx, key); err != nil {
			return UpdateCacheDemoResult{}, fmt.Errorf("invalidate %s: %w", routeID, err)
		}
	}
	return UpdateCacheDemoResult{Invalidated: true}, nil
}
