package loaderdata_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/akashKarmakar02/meta/loaderdata"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
)

// Distinct named types that share a field shape must not be interchangeable.
type structA struct{ ID int64 }
type structB struct{ ID int64 }

type namedInt int

func TestGetReturnsTypedResult(t *testing.T) {
	val := structA{ID: 7}
	ctx := loaderdata.WithContext(context.Background(), val)

	got, ok := loaderdata.Get[structA](ctx)
	if !ok || got.ID != 7 {
		t.Fatalf("Get = %+v, %v; want structA{7}, true", got, ok)
	}
}

func TestGetSliceResult(t *testing.T) {
	val := []string{"one", "two"}
	ctx := loaderdata.WithContext(context.Background(), val)

	got, ok := loaderdata.Get[[]string](ctx)
	if !ok || len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("Get = %v, %v; want [one two], true", got, ok)
	}
}

func TestGetEmptyStructIsDistinguishableFromAbsent(t *testing.T) {
	// An empty struct is a legitimate result, not "no data".
	ctx := loaderdata.WithContext(context.Background(), structA{})
	if got, ok := loaderdata.Get[structA](ctx); !ok || got.ID != 0 {
		t.Fatalf("empty struct: Get = %+v, %v; want {}, true", got, ok)
	}
	if !loaderdata.Has(ctx) {
		t.Fatal("Has must be true for an empty-struct result")
	}

	absent := context.Background()
	if got, ok := loaderdata.Get[structA](absent); ok {
		t.Fatalf("absent context: Get = %+v, %v; want ok=false", got, ok)
	}
	if loaderdata.Has(absent) {
		t.Fatal("Has must be false when no loader ran")
	}
}

func TestGetEmptySliceIsDistinguishableFromAbsent(t *testing.T) {
	ctx := loaderdata.WithContext(context.Background(), []string{})
	if got, ok := loaderdata.Get[[]string](ctx); !ok || got == nil {
		t.Fatalf("empty slice: Get = %v, %v; want non-nil empty, true", got, ok)
	}
	if !loaderdata.Has(ctx) {
		t.Fatal("Has must be true for an empty-slice result")
	}
}

func TestGetNilPointerResult(t *testing.T) {
	// A loader whose type is a pointer may legitimately return nil.
	var ptr *structA
	ctx := loaderdata.WithContext(context.Background(), ptr)
	got, ok := loaderdata.Get[*structA](ctx)
	if !ok || got != nil {
		t.Fatalf("nil pointer: Get = %v, %v; want nil, true", got, ok)
	}
	if !loaderdata.Has(ctx) {
		t.Fatal("Has must be true for a nil-pointer result")
	}
}

func TestGetWrongTypeDoesNotSucceed(t *testing.T) {
	ctx := loaderdata.WithContext(context.Background(), structA{ID: 1})

	// A different type must not succeed merely because the fields match.
	if got, ok := loaderdata.Get[structB](ctx); ok {
		t.Fatalf("structB: Get = %+v, %v; want ok=false", got, ok)
	}
	// A different named type must not succeed either.
	if got, ok := loaderdata.Get[namedInt](ctx); ok {
		t.Fatalf("namedInt: Get = %v, %v; want ok=false", got, ok)
	}
	// Has still reports that a loader ran, distinguishing wrong-type from absent.
	if !loaderdata.Has(ctx) {
		t.Fatal("Has must be true even when the requested type is wrong")
	}
}

func TestGetNamedTypeMatchesExactType(t *testing.T) {
	val := namedInt(5)
	ctx := loaderdata.WithContext(context.Background(), val)
	if got, ok := loaderdata.Get[namedInt](ctx); !ok || got != 5 {
		t.Fatalf("namedInt: Get = %v, %v; want 5, true", got, ok)
	}
	// The underlying int is a different type and must not match.
	if _, ok := loaderdata.Get[int](ctx); ok {
		t.Fatal("int must not match a namedInt result")
	}
}

func TestRepeatedReadsFromSameContext(t *testing.T) {
	ctx := loaderdata.WithContext(context.Background(), structA{ID: 3})
	for i := 0; i < 3; i++ {
		got, ok := loaderdata.Get[structA](ctx)
		if !ok || got.ID != 3 {
			t.Fatalf("read %d: Get = %+v, %v", i, got, ok)
		}
	}
}

func TestContextsAreIsolated(t *testing.T) {
	base := context.Background()
	a := loaderdata.WithContext(base, structA{ID: 1})
	b := loaderdata.WithContext(base, structA{ID: 2})

	if got, _ := loaderdata.Get[structA](a); got.ID != 1 {
		t.Fatalf("a: got %+v, want ID 1", got)
	}
	if got, _ := loaderdata.Get[structA](b); got.ID != 2 {
		t.Fatalf("b: got %+v, want ID 2", got)
	}
	// The base context never gains a value.
	if loaderdata.Has(base) {
		t.Fatal("base context must not carry a loader result")
	}
}

func TestConcurrentIndependentRequests(t *testing.T) {
	var wg sync.WaitGroup
	fail := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := loaderdata.WithContext(context.Background(), structA{ID: int64(i)})
			got, ok := loaderdata.Get[structA](ctx)
			if !ok || got.ID != int64(i) {
				fail <- errors.New("wrong value for goroutine")
			}
		}(i)
	}
	wg.Wait()
	close(fail)
	for err := range fail {
		t.Error(err)
	}
}

func TestCoexistsWithRouteContextAndParams(t *testing.T) {
	ctx := context.Background()
	ctx = routeparams.WithContext(ctx, routeparams.NewParams(map[string]string{"userId": "42"}))
	ctx = routecontext.WithContext(ctx, routecontext.Route{
		RouteID: "users/[userId]/posts/[postId]/page.templ",
		Pattern: "/users/[userId]/posts/[postId]",
		Params:  routeparams.NewParams(map[string]string{"userId": "42"}),
	})
	ctx = loaderdata.WithContext(ctx, structA{ID: 99})

	// All three categories are readable from one context.
	if id, err := routeparams.FromContext(ctx).Int64("userId"); err != nil || id != 42 {
		t.Fatalf("params: %d %v", id, err)
	}
	route, ok := routecontext.FromContext(ctx)
	if !ok || route.RouteID != "users/[userId]/posts/[postId]/page.templ" {
		t.Fatalf("route context: %+v %v", route, ok)
	}
	if got, ok := loaderdata.Get[structA](ctx); !ok || got.ID != 99 {
		t.Fatalf("loader data: %+v %v", got, ok)
	}
}

func TestNilContextIsSafe(t *testing.T) {
	var ctx context.Context
	if _, ok := loaderdata.Get[structA](ctx); ok {
		t.Fatal("nil context: Get must report ok=false")
	}
	if loaderdata.Has(ctx) {
		t.Fatal("nil context: Has must be false")
	}
}
