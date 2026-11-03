package render

import (
	"context"
	"strings"
	"testing"

	"go-meta/loaderdata"
	"go-meta/routecontext"
	"go-meta/routeparams"
)

// TestLoaderDataCoexistsWithRouteContextAndParams proves a single templ
// component can read the route context, the typed params, and the loader
// result from the same render context — the C03 requirement that the three
// categories coexist without one replacing another.
func TestLoaderDataCoexistsWithRouteContextAndParams(t *testing.T) {
	params := routeparams.NewParams(map[string]string{"userId": "42", "postId": "7"})
	ctx := context.Background()
	ctx = routeparams.WithContext(ctx, params)
	ctx = routecontext.WithContext(ctx, routecontext.Route{
		RouteID: "users/[userId]/posts/[postId]/page.templ",
		Pattern: "/users/[userId]/posts/[postId]",
		Params:  params,
	})
	ctx = loaderdata.WithContext(ctx, loaderDataProbe{ID: 7, Title: "Hello"})

	html, err := RenderComponent(ctx, loaderDataComponent())
	if err != nil {
		t.Fatal(err)
	}
	body := string(html)
	for _, want := range []string{
		`<span data-route-id>users/[userId]/posts/[postId]/page.templ</span>`,
		`<span data-user-id>42</span>`,
		`<span data-user-id-error></span>`,
		`<span data-post>id=7 title=Hello</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("render missing %q in: %s", want, body)
		}
	}
}

// TestLoaderDataAbsentWhenNoLoaderRan proves the component degrades to an
// explicit "absent" state when no loader result is attached, while route
// context and params remain available.
func TestLoaderDataAbsentWhenNoLoaderRan(t *testing.T) {
	params := routeparams.NewParams(map[string]string{"userId": "9"})
	ctx := context.Background()
	ctx = routeparams.WithContext(ctx, params)
	ctx = routecontext.WithContext(ctx, routecontext.Route{
		RouteID: "page.templ",
		Pattern: "/",
		Params:  params,
	})

	html, err := RenderComponent(ctx, loaderDataComponent())
	if err != nil {
		t.Fatal(err)
	}
	body := string(html)
	if !strings.Contains(body, `<span data-post>absent</span>`) {
		t.Errorf("expected absent post, got: %s", body)
	}
	if !strings.Contains(body, `<span data-user-id>9</span>`) {
		t.Errorf("params should still render without loader data: %s", body)
	}
}
