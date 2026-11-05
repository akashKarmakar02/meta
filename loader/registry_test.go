package loader_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
)

func TestRegistryTypedBindings(t *testing.T) {
	const users = "users/[userId]/posts/[postId]/page.templ"
	const docs = "docs/[...slug]/page.svelte"
	var calls atomic.Int32
	a := func(_ context.Context, r routecontext.Route) (struct{ ID string }, error) {
		calls.Add(1)
		return struct{ ID string }{r.RouteID}, nil
	}
	b := func(context.Context, routecontext.Route) ([]string, error) {
		calls.Add(1)
		return []string{"one", "two"}, nil
	}
	registry, err := loader.NewRegistry([]string{users, docs, "page.templ"}, loader.Bind(users, a), loader.Bind(docs, b))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("executed during registration")
	}
	for _, id := range []string{users, docs} {
		if _, ok := registry.Lookup(id); !ok {
			t.Fatalf("missing %s", id)
		}
	}
	for _, id := range []string{"page.templ", "missing"} {
		if _, ok := registry.Lookup(id); ok {
			t.Fatalf("unexpected %s", id)
		}
	}
	if !registry.HasRoute("page.templ") || registry.HasRoute("missing") {
		t.Fatal("known and unknown routes not distinguished")
	}
	if calls.Load() != 0 {
		t.Fatal("executed during lookup")
	}
	handle, _ := registry.Lookup(users)
	got, err := handle.Execute(context.Background(), routecontext.Route{RouteID: users})
	if err != nil || got.(struct{ ID string }).ID != users {
		t.Fatalf("%v %v", got, err)
	}
	other, _ := registry.Lookup(docs)
	list, err := other.Execute(context.Background(), routecontext.Route{RouteID: docs})
	if err != nil || len(list.([]string)) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if _, err := handle.Execute(context.Background(), routecontext.Route{RouteID: docs}); !errors.Is(err, loader.ErrRouteMismatch) {
		t.Fatalf("mismatch=%v", err)
	}
}

func TestRegistryValidation(t *testing.T) {
	load := loader.Loader[int](func(context.Context, routecontext.Route) (int, error) { return 0, nil })
	for _, tc := range []struct {
		name     string
		ids      []string
		bindings []loader.Binding
		want     error
	}{
		{"empty route", []string{""}, nil, loader.ErrInvalidRouteID},
		{"duplicate route", []string{"page.templ", "page.templ"}, nil, loader.ErrDuplicateRoute},
		{"empty binding", []string{"page.templ"}, []loader.Binding{{}}, loader.ErrInvalidRouteID},
		{"unknown binding", []string{"page.templ"}, []loader.Binding{loader.Bind("missing/page.templ", load)}, loader.ErrUnknownRoute},
		{"nil loader", []string{"page.templ"}, []loader.Binding{loader.Bind[int]("page.templ", nil)}, loader.ErrNilLoader},
		{"duplicate binding", []string{"page.templ"}, []loader.Binding{loader.Bind("page.templ", load), loader.Bind("page.templ", load)}, loader.ErrDuplicateBinding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := loader.NewRegistry(tc.ids, tc.bindings...)
			if r != nil || !errors.Is(err, tc.want) {
				t.Fatalf("registry=%v error=%v", r, err)
			}
		})
	}
}

func TestRegistryConcurrentLookupAndIsolation(t *testing.T) {
	const id = "a/[id]/page.templ"
	binding := loader.Bind(id, func(context.Context, routecontext.Route) (int, error) { return 7, nil })
	a, err := loader.NewRegistry([]string{id}, binding)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loader.NewRegistry([]string{id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Lookup(id); ok {
		t.Fatal("registries share state")
	}
	var wg sync.WaitGroup
	fail := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, ok := a.Lookup(id)
			if !ok {
				fail <- errors.New("missing")
				return
			}
			v, err := h.Execute(context.Background(), routecontext.Route{RouteID: id})
			if err != nil || v != 7 {
				fail <- fmt.Errorf("%v %v", v, err)
			}
		}()
	}
	wg.Wait()
	close(fail)
	for err := range fail {
		t.Error(err)
	}
}

func TestRegisteredPreservesErrorsAndCancellation(t *testing.T) {
	cause := fmt.Errorf("wrap: %w", loader.ErrForbidden)
	id := "page.templ"
	r, err := loader.NewRegistry([]string{id}, loader.Bind(id, func(context.Context, routecontext.Route) (int, error) { return 9, cause }))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := r.Lookup(id)
	v, err := h.Execute(context.Background(), routecontext.Route{RouteID: id})
	if v != 9 || err != cause || !errors.Is(err, loader.ErrForbidden) {
		t.Fatalf("%v %v", v, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = h.Execute(ctx, routecontext.Route{RouteID: id})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
