package routing

import (
	"fmt"
	"reflect"
	"testing"
	"testing/fstest"
)

// mixedTree is a mixed SSR/CSR app exercising every discovery feature:
// both page kinds, both layout kinds, nesting, and layout ancestry.
func mixedTree() fstest.MapFS {
	return fstest.MapFS{
		"layout.templ":                   &fstest.MapFile{Data: []byte("templ Root() {}")},
		"page.templ":                     &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":               &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.svelte":        &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/page.svelte":          &fstest.MapFile{Data: []byte("<main />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
		"shop/catalog/item/page.svelte":  &fstest.MapFile{Data: []byte("<main />")},
	}
}

// TestDiscoverDeterminism is the B11 acceptance test: repeated discovery
// over the same tree yields byte-identical route descriptors — route order
// and layout order included. Run with -count=100 to shake out any map
// iteration or ordering nondeterminism.
func TestDiscoverDeterminism(t *testing.T) {
	fsys := mixedTree()

	first, err := Discover(fsys)
	if err != nil {
		t.Fatalf("initial Discover: %v", err)
	}
	if len(first) != 5 {
		t.Fatalf("fixture routes = %d, want 5; got %+v", len(first), first)
	}

	for i := 0; i < 100; i++ {
		got, err := Discover(fsys)
		if err != nil {
			t.Fatalf("Discover iteration %d: %v", i, err)
		}
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("iteration %d differs:\nfirst: %+v\ngot:   %+v", i, first, got)
		}
	}
}

// TestDiscoverSnapshot pins the complete descriptor set as a golden string:
// order of routes, order of layouts, every field. Any change in output
// shape or ordering must show up here deliberately.
func TestDiscoverSnapshot(t *testing.T) {
	routes, err := Discover(mixedTree())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Golden snapshot: root layout.templ wraps every route (ancestry walks
	// to the app root), dashboard adds its svelte layout for its subtree.
	want := `[/ ssr> ssr page.templ [layout.templ ] /about ssr> ssr about/page.templ [layout.templ ] /dashboard csr> csr dashboard/page.svelte [dashboard/layout.svelte ] /dashboard/settings csr> csr dashboard/settings/page.svelte [dashboard/layout.svelte ] /shop/catalog/item ssr> csr shop/catalog/item/page.svelte [layout.templ ]]`
	got := fmt.Sprint(describeRoutes(routes))
	if got != want {
		t.Errorf("snapshot mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

// describeRoutes renders the full descriptor set into a comparable string.
func describeRoutes(routes []Route) []string {
	var out []string
	for _, r := range routes {
		s := fmt.Sprintf("%s %s %s %s [", r.Path, joinModes(r.Layouts), r.Mode, r.SourceFile)
		for _, l := range r.Layouts {
			s += l.SourceFile + " "
		}
		s += "]"
		out = append(out, s)
	}
	return out
}

func joinModes(layouts []Layout) string {
	s := ""
	for _, l := range layouts {
		s += string(l.Mode) + ">"
	}
	return s
}
