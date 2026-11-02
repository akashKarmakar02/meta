package routing

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRoutingValidFixture(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":                   {},
		"page.templ":                     {},
		"About/page.templ":               {},
		"about/page.templ":               {},
		"dashboard/layout.svelte":        {},
		"dashboard/page.svelte":          {},
		"dashboard/settings/page.svelte": {},
		"products/new/page.svelte":       {},
		"products/[id]/page.svelte":      {},
		"dashboard/card.svelte":          {},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	type descriptor struct {
		path, source string
		mode         RenderMode
		layouts      []string
	}
	got := make([]descriptor, 0, len(routes))
	for _, route := range routes {
		ids := make([]string, 0, len(route.Layouts))
		for _, layout := range route.Layouts {
			ids = append(ids, layout.ID)
		}
		got = append(got, descriptor{route.Path, route.SourceFile, route.Mode, ids})
	}
	root := []string{"app/layout.templ"}
	dashboard := []string{"app/layout.templ", "app/dashboard/layout.svelte"}
	want := []descriptor{
		{"/", "page.templ", ModeSSR, root},
		{"/About", "About/page.templ", ModeSSR, root},
		{"/about", "about/page.templ", ModeSSR, root},
		{"/dashboard", "dashboard/page.svelte", ModeCSR, dashboard},
		{"/dashboard/settings", "dashboard/settings/page.svelte", ModeCSR, dashboard},
		{"/products/new", "products/new/page.svelte", ModeCSR, root},
		{"/products/:id", "products/[id]/page.svelte", ModeCSR, root},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("discovered descriptors = %#v, want %#v", got, want)
	}
}

func TestRoutingInvalidFixtures(t *testing.T) {
	cases := []struct {
		name  string
		files fstest.MapFS
		want  []string
	}{
		{"no pages", fstest.MapFS{"card.svelte": {}}, []string{"no routes found"}},
		{"duplicate pages", fstest.MapFS{"about/page.templ": {}, "about/page.svelte": {}}, []string{"duplicate route", "/about", "page.templ", "page.svelte"}},
		{"conflicting layouts", fstest.MapFS{"page.templ": {}, "layout.templ": {}, "layout.svelte": {}}, []string{"conflicting layouts", "layout.templ", "layout.svelte"}},
		{"reserved static", fstest.MapFS{"page.templ": {}, "blog/page.static.templ": {}}, []string{"STATIC_NOT_IMPLEMENTED", "blog/page.static.templ"}},
		{"ambiguous parameters", fstest.MapFS{"products/[id]/page.svelte": {}, "products/[slug]/page.svelte": {}}, []string{"ambiguous", "id", "slug"}},
		{"server page beneath client layout", fstest.MapFS{"dashboard/layout.svelte": {}, "dashboard/settings/page.templ": {}}, []string{"SSR page", "dashboard/settings/page.templ", "dashboard/layout.svelte"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes, err := Discover(tc.files)
			if err == nil {
				t.Fatalf("Discover returned routes %+v, want error", routes)
			}
			for _, part := range tc.want {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("error %q does not contain %q", err, part)
				}
			}
		})
	}
}
