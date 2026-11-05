package routing_test

import (
	"encoding/json"
	"testing"

	"github.com/akashKarmakar02/meta/routing"
)

func TestPublicRouteContract(t *testing.T) {
	layout, err := routing.NewLayout("app/layout.templ", "layout.templ", routing.ModeSSR)
	if err != nil {
		t.Fatal(err)
	}
	layouts := []routing.Layout{layout}
	route, err := routing.NewRoute("/docs/+", "docs/[...slug]/page.templ", routing.ModeSSR, layouts)
	if err != nil {
		t.Fatal(err)
	}
	route.CatchAll = "slug"
	layouts[0].ID = "changed"
	if route.ID != "docs/[...slug]/page.templ" || route.Layouts[0].ID != "app/layout.templ" {
		t.Fatalf("route ID or copied layout: %+v", route)
	}
	data, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"docs/[...slug]/page.templ","path":"/docs/+","sourceFile":"docs/[...slug]/page.templ","mode":"ssr","layouts":[{"id":"app/layout.templ","sourceFile":"layout.templ","mode":"ssr"}],"catchAll":"slug"}`
	if string(data) != want {
		t.Fatalf("JSON = %s, want %s", data, want)
	}
}

func TestPublicRouteValidation(t *testing.T) {
	if !routing.ModeSSR.Valid() || !routing.ModeCSR.Valid() || routing.RenderMode("static").Valid() {
		t.Fatal("unexpected render mode validity")
	}
	if _, err := routing.NewLayout("", "layout.templ", routing.ModeSSR); err == nil {
		t.Fatal("accepted empty layout ID")
	}
	if _, err := routing.NewRoute("relative", "page.templ", routing.ModeSSR, nil); err == nil {
		t.Fatal("accepted relative route")
	}
}
