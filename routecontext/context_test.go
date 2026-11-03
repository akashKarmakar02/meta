package routecontext_test

import (
	"context"
	"net/url"
	"testing"

	"go-meta/routecontext"
	"go-meta/routeparams"
)

func TestQuerySnapshot(t *testing.T) {
	source := url.Values{"tag": {"go", "web"}, "empty": {""}}
	query := routecontext.NewQuery(source)
	source["tag"][0] = "changed"
	if query.Get("tag") != "go" || !query.Has("empty") || query.Has("missing") {
		t.Fatalf("unexpected query: %#v", query.All())
	}
	values := query.Values("tag")
	values[0] = "changed"
	all := query.All()
	all["tag"][1] = "changed"
	if got := query.Values("tag"); len(got) != 2 || got[0] != "go" || got[1] != "web" {
		t.Fatalf("mutated query: %v", got)
	}
	var empty routecontext.Query
	if empty.Get("x") != "" || empty.Has("x") || len(empty.All()) != 0 {
		t.Fatal("unsafe zero query")
	}
}

func TestContextAccess(t *testing.T) {
	if _, ok := routecontext.FromContext(nil); ok {
		t.Fatal("nil context has route")
	}
	if _, ok := routecontext.FromContext(context.Background()); ok {
		t.Fatal("background has route")
	}
	if _, ok := routecontext.FromContext(context.WithValue(context.Background(), "routecontext", routecontext.Route{})); ok {
		t.Fatal("string key must not supply a route context")
	}
	params := routeparams.NewParams(map[string]string{"id": "42"})
	input := routecontext.Route{RouteID: "users/[id]/page.templ", Pattern: "/users/[id]", Params: params, Query: routecontext.NewQuery(url.Values{"q": {"hello"}})}
	ctx := routecontext.WithContext(context.Background(), input)
	got, ok := routecontext.FromContext(ctx)
	if !ok || got.RouteID != input.RouteID || got.Pattern != input.Pattern || got.Query.Get("q") != "hello" {
		t.Fatalf("got %+v, %v", got, ok)
	}
	id, err := got.Params.Int64("id")
	if err != nil || id != 42 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}
