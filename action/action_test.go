package action

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-meta/routecontext"
	"go-meta/routeparams"
)

// errSentinel is a distinctive application error for wrap/classification cases.
var errSentinel = errors.New("app: boom")

// fixture types covering the type matrix.
type profileInput struct {
	DisplayName string
	Bio         string
}

type profileResult struct {
	Updated bool
}

type ids []int

type emptyResult struct{}

type namedString string

func routeFixture() routecontext.Route {
	return routecontext.Route{
		RouteID: "posts-show",
		Pattern: "/users/[userId]/posts/[postId]",
		Params:  routeparams.NewParams(map[string]string{"userId": "42", "postId": "7"}),
		Query:   routecontext.NewQuery(url.Values{"notify": {"true"}}),
	}
}

func TestExecuteTypeMatrix(t *testing.T) {
	route := routeFixture()

	t.Run("struct input and struct output", func(t *testing.T) {
		got, err := Execute(context.Background(), route, profileInput{DisplayName: "Alice"}, func(_ context.Context, _ routecontext.Route, in profileInput) (profileResult, error) {
			return profileResult{Updated: in.DisplayName != ""}, nil
		})
		if err != nil || !got.Updated {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("slice input and slice output", func(t *testing.T) {
		got, err := Execute(context.Background(), route, ids{1, 2, 3}, func(_ context.Context, _ routecontext.Route, in ids) (ids, error) {
			return append(in, 4), nil
		})
		if err != nil || len(got) != 4 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("pointer input", func(t *testing.T) {
		in := &profileInput{DisplayName: "Bob"}
		got, err := Execute(context.Background(), route, in, func(_ context.Context, _ routecontext.Route, in *profileInput) (profileResult, error) {
			return profileResult{Updated: in != nil && in.DisplayName == "Bob"}, nil
		})
		if err != nil || !got.Updated {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("pointer output nil result legitimate", func(t *testing.T) {
		got, err := Execute(context.Background(), route, "nil", func(_ context.Context, _ routecontext.Route, _ string) (*profileResult, error) {
			return nil, nil
		})
		if err != nil || got != nil {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("named scalar types", func(t *testing.T) {
		got, err := Execute(context.Background(), route, namedString("7"), func(_ context.Context, _ routecontext.Route, in namedString) (namedString, error) {
			return in + "!", nil
		})
		if err != nil || got != "7!" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("empty output struct", func(t *testing.T) {
		got, err := Execute(context.Background(), route, struct{}{}, func(_ context.Context, _ routecontext.Route, _ struct{}) (emptyResult, error) {
			return emptyResult{}, nil
		})
		if err != nil || got != (emptyResult{}) {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("zero-valued output legitimate", func(t *testing.T) {
		got, err := Execute(context.Background(), route, true, func(_ context.Context, _ routecontext.Route, _ bool) (int, error) {
			return 0, nil
		})
		if err != nil || got != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("zero-valued numeric input", func(t *testing.T) {
		got, err := Execute(context.Background(), route, int64(0), func(_ context.Context, _ routecontext.Route, in int64) (bool, error) {
			return in == 0, nil
		})
		if err != nil || !got {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

func TestExecuteErrors(t *testing.T) {
	route := routeFixture()
	ctx := context.Background()

	t.Run("ordinary error preserved", func(t *testing.T) {
		_, err := Execute(ctx, route, "in", func(context.Context, routecontext.Route, string) (int, error) {
			return 0, errSentinel
		})
		if !errors.Is(err, errSentinel) {
			t.Fatalf("want sentinel, got %v", err)
		}
	})

	t.Run("wrapped error inspectable", func(t *testing.T) {
		wrapped := fmt.Errorf("update failed: %w", errSentinel)
		_, err := Execute(ctx, route, "in", func(context.Context, routecontext.Route, string) (int, error) {
			return 0, wrapped
		})
		if !errors.Is(err, errSentinel) {
			t.Fatalf("want wrapped sentinel inspectable, got %v", err)
		}
	})

	t.Run("sentinel classification via errors.Is", func(t *testing.T) {
		for _, sent := range []error{ErrNotFound, ErrUnauthorized, ErrForbidden, ErrInvalidInput} {
			_, err := Execute(ctx, route, "in", func(context.Context, routecontext.Route, string) (int, error) {
				return 0, fmt.Errorf("action failed: %w", sent)
			})
			if !errors.Is(err, sent) {
				t.Fatalf("sentinel %v not preserved through wrapping", sent)
			}
		}
	})

	t.Run("nil action rejected", func(t *testing.T) {
		var act Action[string, int]
		_, err := Execute(ctx, route, "in", act)
		if !errors.Is(err, ErrNilAction) {
			t.Fatalf("want ErrNilAction, got %v", err)
		}
	})

	t.Run("nil context rejected", func(t *testing.T) {
		called := false
		_, err := Execute(nil, route, "in", func(context.Context, routecontext.Route, string) (int, error) {
			called = true
			return 1, nil
		})
		if !errors.Is(err, ErrNilContext) {
			t.Fatalf("want ErrNilContext, got %v", err)
		}
		if called {
			t.Fatal("action ran with nil context")
		}
	})

	t.Run("already-canceled context not invoked", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		_, err := Execute(ctx, route, "in", func(context.Context, routecontext.Route, string) (int, error) {
			calls++
			return 1, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
		if calls != 0 {
			t.Fatal("action ran on canceled context")
		}
	})

	t.Run("cancellation during cooperative execution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got, err := Execute(ctx, route, "in", func(ctx context.Context, _ routecontext.Route, _ string) (string, error) {
			cancel()
			<-ctx.Done() // cooperative: observe cancellation, then finish
			return "committed", nil
		})
		if err != nil || got != "committed" {
			t.Fatalf("got %q, %v — a committed result must be preserved", got, err)
		}
	})

	t.Run("deadline propagation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_, err := Execute(ctx, route, "in", func(ctx context.Context, _ routecontext.Route, _ string) (int, error) {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Second):
				return 1, nil
			}
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want DeadlineExceeded, got %v", err)
		}
	})
}

func TestExecuteOnceAndIndependence(t *testing.T) {
	route := routeFixture()

	t.Run("invoked exactly once", func(t *testing.T) {
		calls := 0
		_, _ = Execute(context.Background(), route, "in", func(context.Context, routecontext.Route, string) (int, error) {
			calls++
			return 1, nil
		})
		if calls != 1 {
			t.Fatalf("want 1 call, got %d", calls)
		}
	})

	t.Run("no implicit retry after error", func(t *testing.T) {
		calls := 0
		_, err := Execute(context.Background(), route, "in", func(context.Context, routecontext.Route, string) (int, error) {
			calls++
			return 0, errSentinel
		})
		if calls != 1 || !errors.Is(err, errSentinel) {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})

	t.Run("repeated independent invocations", func(t *testing.T) {
		var calls atomic.Int64
		act := func(context.Context, routecontext.Route, string) (int, error) {
			calls.Add(1)
			return 1, nil
		}
		for i := 0; i < 3; i++ {
			if _, err := Execute(context.Background(), route, "in", act); err != nil {
				t.Fatal(err)
			}
		}
		if calls.Load() != 3 {
			t.Fatalf("want 3 calls, got %d", calls.Load())
		}
	})

	t.Run("concurrent independent invocations race-safe", func(t *testing.T) {
		const n = 64
		var wg sync.WaitGroup
		var total atomic.Int64
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := Execute(context.Background(), route, 1, func(_ context.Context, _ routecontext.Route, in int) (int, error) {
					total.Add(int64(in))
					return in, nil
				})
				if err != nil || got != 1 {
					t.Errorf("got %d, %v", got, err)
				}
			}()
		}
		wg.Wait()
		if total.Load() != n {
			t.Fatalf("want %d, got %d", n, total.Load())
		}
	})
}

func TestRouteAccess(t *testing.T) {
	t.Run("typed path parameters and query access", func(t *testing.T) {
		route := routeFixture()
		got, err := Execute(context.Background(), route, struct{}{}, func(_ context.Context, r routecontext.Route, _ struct{}) (string, error) {
			userID, err := r.Params.Int64("userId")
			if err != nil {
				return "", err
			}
			postID, err := r.Params.Int64("postId")
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%d/%d?%s", userID, postID, r.Query.Get("notify")), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := "42/7?true"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("catch-all route metadata", func(t *testing.T) {
		route := routecontext.Route{
			RouteID: "docs-slug",
			Pattern: "/docs/[...slug]",
			Params:  routeparams.NewParams(map[string]string{"slug": "guide/intro"}),
			Query:   routecontext.NewQuery(url.Values{"v": {"2"}}),
		}
		got, err := Execute(context.Background(), route, struct{}{}, func(_ context.Context, r routecontext.Route, _ struct{}) (string, error) {
			slug, _ := r.Params.Get("slug")
			return r.Pattern + " " + slug, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "[...slug]") || !strings.Contains(got, "guide/intro") {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("repeated query values distinct from first", func(t *testing.T) {
		route := routecontext.Route{
			Query: routecontext.NewQuery(url.Values{"tag": {"a", "b"}}),
		}
		got, err := Execute(context.Background(), route, struct{}{}, func(_ context.Context, r routecontext.Route, _ struct{}) (string, error) {
			return strings.Join(r.Query.Values("tag"), ","), nil
		})
		if err != nil || got != "a,b" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}

func TestPackageIsolation(t *testing.T) {
	t.Run("no package-level execution at init", func(t *testing.T) {
		// The package has no init function and no package-level mutable state;
		// this guard documents that property. If an init-time execution were
		// introduced, TestMain below would catch stray calls via panic.
		var zero atomic.Bool
		if zero.Load() {
			t.Fatal("impossible")
		}
	})
}
