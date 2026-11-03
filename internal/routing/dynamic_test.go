package routing

import (
	"fmt"
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

func TestTooManyDynamicSegmentsRejected(t *testing.T) {
	parts := make([]string, 31)
	for i := range parts {
		parts[i] = fmt.Sprintf("[p%d]", i)
	}
	_, err := Discover(fstest.MapFS{strings.Join(parts, "/") + "/page.svelte": {}})
	if err == nil || !strings.Contains(err.Error(), "at most 30") {
		t.Fatalf("expected parameter limit error, got %v", err)
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

// The same name may appear in different routes, and distinct names can occur
// at any number of positions in a route.
func TestSameParameterNameDifferentPositionsValid(t *testing.T) {
	fsys := fstest.MapFS{
		"products/[id]/page.svelte":   {Data: []byte("x")},
		"categories/[id]/page.svelte": {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("route count = %d, want 2", len(routes))
	}

	twoParams := fstest.MapFS{
		"products/[id]/edit/[step]/page.svelte": {Data: []byte("x")},
	}
	if _, err := Discover(twoParams); err != nil {
		t.Fatalf("two dynamic segments: %v", err)
	}

	yearSlashSlug := fstest.MapFS{
		"blog/[year]/[slug]/page.svelte": {Data: []byte("x")},
	}
	if _, err := Discover(yearSlashSlug); err != nil {
		t.Fatalf("adjacent dynamic segments: %v", err)
	}
}

func TestNestedDynamicConflicts(t *testing.T) {
	for _, tc := range []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"equivalent", fstest.MapFS{"users/[id]/posts/[post]/page.svelte": {}, "users/[name]/posts/[slug]/page.svelte": {}}, "ambiguous"},
		{"equivalent mixed modes", fstest.MapFS{"users/[id]/posts/[post]/page.templ": {}, "users/[name]/posts/[slug]/page.svelte": {}}, "ambiguous"},
		{"duplicate name", fstest.MapFS{"users/[id]/posts/[id]/page.svelte": {}}, "duplicate parameter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Discover(tc.fsys)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Discover() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSSRPageInBracketDirectoryAllowed(t *testing.T) {
	fsys := fstest.MapFS{
		"products/[id]/page.templ": {Data: []byte("package app")},
	}
	if _, err := Discover(fsys); err != nil {
		t.Fatalf("Discover: %v", err)
	}
}

func TestGoSourceInsideDynamicDirectoryRejected(t *testing.T) {
	fsys := fstest.MapFS{"users/[id]/page.templ": {}, "users/[id]/helpers.go": {}}
	_, err := Discover(fsys)
	if err == nil || !strings.Contains(err.Error(), "helpers.go") || !strings.Contains(err.Error(), "move helpers") {
		t.Fatalf("Discover error = %v", err)
	}
}
