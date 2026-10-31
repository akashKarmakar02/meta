package routing

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestDiscoverDynamicRouteNormalization checks that a bracketed directory
// segment becomes a Fiber parameter segment: /products/[id]/page.svelte
// must be represented as route path "/products/:id".
func TestDiscoverDynamicRouteNormalization(t *testing.T) {
	fsys := fstest.MapFS{
		"products/new/page.svelte":  {Data: []byte("x")},
		"products/[id]/page.svelte": {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	want := []struct{ path, source string }{
		{"/products/new", "products/new/page.svelte"},
		{"/products/:id", "products/[id]/page.svelte"},
	}
	if len(routes) != len(want) {
		t.Fatalf("Discover() route count = %d, want %d (%+v)", len(routes), len(want), routes)
	}
	for i, w := range want {
		if routes[i].Path != w.path {
			t.Errorf("routes[%d].Path = %q, want %q", i, routes[i].Path, w.path)
		}
		if routes[i].SourceFile != w.source {
			t.Errorf("routes[%d].SourceFile = %q, want %q", i, routes[i].SourceFile, w.source)
		}
		if routes[i].Mode != ModeCSR {
			t.Errorf("routes[%d].Mode = %q, want %q", i, routes[i].Mode, ModeCSR)
		}
	}
}

// TestStaticPathPriorityOverDynamic is C08's acceptance criterion: when both
// /products/new (static) and /products/[id] (dynamic) exist, the static
// path must win for the URL /products/new. With deterministic registration
// order (static sorted before ":id" because "/" < ":"), Fiber registers the
// static route first and matches it before the parameterized one.
func TestStaticPathPriorityOverDynamic(t *testing.T) {
	fsys := fstest.MapFS{
		"products/new/page.svelte":  {Data: []byte("x")},
		"products/[id]/page.svelte": {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	newIdx, dynIdx := -1, -1
	for i, r := range routes {
		switch r.Path {
		case "/products/new":
			newIdx = i
		case "/products/:id":
			dynIdx = i
		}
	}
	if newIdx == -1 || dynIdx == -1 {
		t.Fatalf("missing routes: static=%d dynamic=%d (%+v)", newIdx, dynIdx, routes)
	}
	if newIdx > dynIdx {
		t.Errorf("static route /products/new (index %d) must be registered before dynamic /products/:id (index %d) so Fiber matches static paths first",
			newIdx, dynIdx)
	}
}

func TestAmbiguousDynamicSegmentsRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"products/[id]/page.svelte":   {Data: []byte("x")},
		"products/[slug]/page.svelte": {Data: []byte("x")},
	}
	_, err := Discover(fsys)
	if err == nil {
		t.Fatal("Discover() expected ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention ambiguity, got: %v", err)
	}
	if !strings.Contains(err.Error(), "products") {
		t.Errorf("error should name the colliding parent path, got: %v", err)
	}
}

func TestSameParameterNameDifferentPositionsValid(t *testing.T) {
	fsys := fstest.MapFS{
		"products/[id]/page.svelte":             {Data: []byte("x")},
		"categories/[id]/page.svelte":           {Data: []byte("x")},
		"products/[id]/edit/[step]/page.svelte": {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("route count = %d, want 3", len(routes))
	}
}

func TestSSRPageInBracketDirectoryRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"products/[id]/page.templ": {Data: []byte("package app")},
	}
	_, err := Discover(fsys)
	if err == nil {
		t.Fatal("Discover() expected SSR-in-bracket rejection, got nil")
	}
	if !strings.Contains(err.Error(), "import paths cannot contain") {
		t.Errorf("error should explain the Go package constraint, got: %v", err)
	}
	if !strings.Contains(err.Error(), "page.svelte") {
		t.Errorf("error should point to the CSR alternative, got: %v", err)
	}
}
