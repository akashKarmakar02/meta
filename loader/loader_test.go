package loader_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"go-meta/loader"
	"go-meta/routecontext"
	"go-meta/routeparams"
)

type user struct {
	ID   int64
	Tags []string
}

func TestExecuteTypedResultsAndErrors(t *testing.T) {
	ctx := context.Background()
	route := routecontext.Route{RouteID: "users/[id]/page.templ", Pattern: "/users/[id]", Params: routeparams.NewParams(map[string]string{"id": "42"}), Query: routecontext.NewQuery(url.Values{"tag": {"go", "web"}})}
	got, err := loader.Execute(ctx, route, func(_ context.Context, r routecontext.Route) (user, error) {
		id, e := r.Params.Int64("id")
		return user{id, r.Query.Values("tag")}, e
	})
	if err != nil || !reflect.DeepEqual(got, user{42, []string{"go", "web"}}) {
		t.Fatalf("user=%+v err=%v", got, err)
	}
	list, err := loader.Execute(ctx, route, func(context.Context, routecontext.Route) ([]user, error) { return []user{{ID: 42}}, nil })
	if err != nil || len(list) != 1 || list[0].ID != 42 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	empty, err := loader.Execute(ctx, route, func(context.Context, routecontext.Route) ([]user, error) { return []user{}, nil })
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty=%v err=%v", empty, err)
	}
	cause := errors.New("storage failure")
	wrapped := fmt.Errorf("%w: %w", loader.ErrNotFound, cause)
	value, err := loader.Execute(ctx, route, func(context.Context, routecontext.Route) (int, error) { return 7, wrapped })
	if value != 7 || !errors.Is(err, loader.ErrNotFound) || !errors.Is(err, cause) || err != wrapped {
		t.Fatalf("value=%d err=%v", value, err)
	}
}

func TestExecutePreconditions(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		fn   loader.Loader[int]
		want error
	}{
		{"nil context", nil, func(context.Context, routecontext.Route) (int, error) { t.Fatal("called"); return 0, nil }, loader.ErrNilContext},
		{"nil loader", context.Background(), nil, loader.ErrNilLoader},
		{"cancelled", cancelled, func(context.Context, routecontext.Route) (int, error) { t.Fatal("called"); return 0, nil }, context.Canceled},
		{"deadline", deadline, func(context.Context, routecontext.Route) (int, error) { t.Fatal("called"); return 0, nil }, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := loader.Execute(tc.ctx, routecontext.Route{}, tc.fn)
			if v != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("%d %v", v, err)
			}
		})
	}
}

func TestExecuteCooperativeCancellationAndDeadline(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := loader.Execute(ctx, routecontext.Route{}, func(ctx context.Context, _ routecontext.Route) (int, error) {
			if _, ok := ctx.Deadline(); !ok {
				return 0, errors.New("deadline missing")
			}
			close(started)
			<-ctx.Done()
			return 0, ctx.Err()
		})
		done <- err
	}()
	<-started
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	result, err := loader.Execute(ctx2, routecontext.Route{}, func(context.Context, routecontext.Route) (int, error) { cancel2(); return 9, nil })
	if err != nil || result != 9 {
		t.Fatalf("in-flight result=%d err=%v", result, err)
	}
}

func TestConcurrentRouteSnapshots(t *testing.T) {
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			route := routecontext.Route{RouteID: "docs/[id]/[...slug]/page.templ", Pattern: "/docs/[id]/[...slug]", Params: routeparams.NewParams(map[string]string{"id": id, "slug": "intro/install"}), Query: routecontext.NewQuery(url.Values{"tag": {id, "web"}})}
			got, err := loader.Execute(context.Background(), route, func(_ context.Context, r routecontext.Route) (string, error) {
				v, _ := r.Params.Get("id")
				slug, _ := r.Params.Get("slug")
				tags := r.Query.Values("tag")
				tags[0] = "changed"
				if r.Query.Get("tag") != id || r.RouteID != route.RouteID || r.Pattern != route.Pattern {
					return "", errors.New("snapshot mutated")
				}
				return v + ":" + slug, nil
			})
			if err != nil || got != id+":intro/install" {
				errs <- fmt.Errorf("%q: %v", got, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestOutcomeClassification(t *testing.T) {
	for _, sentinel := range []error{loader.ErrNotFound, loader.ErrUnauthorized, loader.ErrForbidden, loader.ErrInvalidInput} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			wrapped := fmt.Errorf("operation failed: %w", sentinel)
			_, err := loader.Execute(context.Background(), routecontext.Route{}, func(context.Context, routecontext.Route) (struct{}, error) { return struct{}{}, wrapped })
			if !errors.Is(err, sentinel) {
				t.Fatalf("classification lost: %v", err)
			}
		})
	}
}

type loaderFailure struct{ reason string }

func (e *loaderFailure) Error() string { return e.reason }

func TestExecuteInvokesOnceAndPreservesErrorType(t *testing.T) {
	calls := 0
	cause := &loaderFailure{reason: "private diagnostic"}
	_, err := loader.Execute(context.Background(), routecontext.Route{}, func(context.Context, routecontext.Route) (int, error) {
		calls++
		return 0, fmt.Errorf("operation: %w", cause)
	})
	var got *loaderFailure
	if calls != 1 || !errors.As(err, &got) || got != cause {
		t.Fatalf("calls=%d error=%v cause=%v", calls, err, got)
	}
}
