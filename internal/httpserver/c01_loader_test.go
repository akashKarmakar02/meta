package httpserver_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/gofiber/fiber/v3"
)

func TestLoaderConsumesMatchedRequestSnapshot(t *testing.T) {
	type result struct {
		User, Post  int64
		Tags        []string
		ID, Pattern string
	}
	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	app := fiber.New()
	app.Use(httpserver.ValidatePagePath)
	app.Use(func(c fiber.Ctx) error { c.SetContext(parent); return c.Next() })
	route := routing.Route{ID: "users/[userId]/posts/[postId]/page.templ", SourceFile: "users/[userId]/posts/[postId]/page.templ", Path: "/users/:userId/posts/:postId"}
	var saved context.Context
	var snapshot routecontext.Route
	var got result
	var loadErr error
	app.Get(route.Path, httpserver.WithRouteParams(route, func(c fiber.Ctx) error {
		saved = c.Context()
		var ok bool
		snapshot, ok = routecontext.FromContext(saved)
		if !ok {
			return errors.New("missing route")
		}
		got, loadErr = loader.Execute(saved, snapshot, func(ctx context.Context, r routecontext.Route) (result, error) {
			if ctx.Err() != nil {
				return result{}, ctx.Err()
			}
			if _, ok := ctx.Deadline(); !ok {
				return result{}, errors.New("missing parent deadline")
			}
			u, err := r.Params.Int64("userId")
			if err != nil {
				return result{}, err
			}
			p, err := r.Params.Int64("postId")
			if err != nil {
				return result{}, err
			}
			return result{u, p, r.Query.Values("tag"), r.RouteID, r.Pattern}, nil
		})
		if loadErr != nil {
			return loadErr
		}
		_, loadErr = loader.Execute(saved, snapshot, func(context.Context, routecontext.Route) (result, error) {
			return result{}, fmt.Errorf("wrapped: %w", loader.ErrForbidden)
		})
		if !errors.Is(loadErr, loader.ErrForbidden) {
			return fmt.Errorf("loader error: %v", loadErr)
		}
		return c.SendStatus(200)
	}))
	res, err := app.Test(httptest.NewRequest("GET", "/users/42/posts/7?tag=go&tag=web", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || !reflect.DeepEqual(got, result{42, 7, []string{"go", "web"}, route.ID, "/users/[userId]/posts/[postId]"}) {
		t.Fatalf("status=%d result=%+v error=%v", res.StatusCode, got, loadErr)
	}
	if saved == nil || !errors.Is(saved.Err(), context.Canceled) || parent.Err() != nil {
		t.Fatalf("dispatch lifecycle ctx=%v parent=%v", saved, parent.Err())
	}
	if snapshot.Query.Get("tag") != "go" {
		t.Fatal("snapshot lost after dispatch")
	}
}
