package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akashKarmakar02/meta/action"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
)

// TestD01ActionContractIntegration exercises the D01 action contract against a
// genuine B05 route snapshot produced by the real framework pipeline.
//
// The snapshot is captured by the page's registered loader — the same
// per-request routecontext.Route the action contract accepts — during an
// ordinary SSR request. No action endpoint is registered, no handler performs
// a mutation, and the action only ever runs through an explicit
// action.Execute call in this test, outside the HTTP pipeline.
func TestD01ActionContractIntegration(t *testing.T) {
	var (
		calls    atomic.Int64
		captured atomic.Pointer[routecontext.Route]
	)

	type renamePostInput struct{ Title string }
	type renamePostResult struct {
		Updated bool
		PostID  int64
	}

	renamePost := func(_ context.Context, r routecontext.Route, in renamePostInput) (renamePostResult, error) {
		calls.Add(1)
		if in.Title == "" {
			return renamePostResult{}, errors.New("title required")
		}
		postID, err := r.Params.Int64("postId")
		if err != nil {
			return renamePostResult{}, err
		}
		return renamePostResult{Updated: true, PostID: postID}, nil
	}

	registry, err := newLoaderRegistry([]loader.Binding{
		loader.Bind("posts/[postId]/page.templ", func(_ context.Context, r routecontext.Route) (int, error) {
			// Keep the first request's snapshot; later requests overwrite.
			captured.CompareAndSwap(nil, &r)
			return 0, nil
		}),
		loader.Bind("products/[id]/page.svelte", func(context.Context, routecontext.Route) (int, error) {
			return 0, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredApp(nil, false, registry)

	// Ordinary SSR document request: captures the snapshot, runs no action.
	code, _ := getBody(t, app, "ssr snapshot", "/posts/42?notify=true", false)
	if code != 200 {
		t.Fatalf("ssr status=%d, want 200", code)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("ordinary GET invoked the action %d times, want 0", got)
	}

	// Navigation-envelope and CSR requests must not invoke it either.
	if code, _ = getBody(t, app, "nav", "/posts/42", true); code != 200 {
		t.Fatalf("nav status=%d", code)
	}
	if code, _ = getBody(t, app, "csr", "/products/7", false); code != 200 {
		t.Fatalf("csr status=%d", code)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("SSR/nav/CSR requests invoked the action %d times, want 0", got)
	}

	// The captured snapshot is a genuine B05 value from the request pipeline.
	snap := captured.Load()
	if snap == nil {
		t.Fatal("loader never captured a route snapshot")
	}
	if snap.RouteID != "posts/[postId]/page.templ" {
		t.Fatalf("snapshot RouteID=%q", snap.RouteID)
	}

	// Typed execution through the public helper, outside any HTTP pipeline.
	out, err := action.Execute(context.Background(), *snap, renamePostInput{Title: "Hello"}, renamePost)
	if err != nil {
		t.Fatalf("action error: %v", err)
	}
	if !out.Updated || out.PostID != 42 {
		t.Fatalf("typed output = %+v, want {Updated:true PostID:42}", out)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("action invoked %d times on one Execute, want 1", got)
	}
	if notify := snap.Query.Get("notify"); notify != "true" {
		t.Fatalf("snapshot query notify=%q, want true", notify)
	}

	// Repeated explicit invocations stay independent and exactly-once each.
	for i := 0; i < 2; i++ {
		out, err := action.Execute(context.Background(), *snap, renamePostInput{Title: "Again"}, renamePost)
		if err != nil || !out.Updated {
			t.Fatalf("repeat %d: out=%+v err=%v", i, out, err)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("action invoked %d times after 3 Execute calls, want 3", got)
	}

	// Application errors are preserved verbatim, no wrapping or conversion.
	if _, err = action.Execute(context.Background(), *snap, renamePostInput{}, renamePost); err == nil || !strings.Contains(err.Error(), "title required") {
		t.Fatalf("application error not preserved: %v", err)
	}

	// Already-canceled context prevents invocation entirely.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := calls.Load()
	if _, err = action.Execute(ctx, *snap, renamePostInput{Title: "x"}, renamePost); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx error=%v, want context.Canceled", err)
	}
	if calls.Load() != before {
		t.Fatal("action ran despite an already-canceled context")
	}

	// Cancellation during execution is cooperative: the action observes the
	// deadline itself and its committed result is preserved, never replayed.
	deadlineCtx, stop := context.WithCancel(context.Background())
	result, execErr := make(chan renamePostResult, 1), make(chan error, 1)
	go func() {
		out, err := action.Execute(deadlineCtx, *snap, renamePostInput{Title: "Final"}, func(ctx context.Context, r routecontext.Route, in renamePostInput) (renamePostResult, error) {
			calls.Add(1)
			stop()
			<-ctx.Done() // cooperative: finish on our own terms
			return renamePostResult{Updated: true, PostID: 99}, nil
		})
		result <- out
		execErr <- err
	}()
	got := <-result
	if err := <-execErr; err != nil || !got.Updated || got.PostID != 99 {
		t.Fatalf("cooperative cancel: out=%+v err=%v — committed result must be preserved", got, err)
	}
}
