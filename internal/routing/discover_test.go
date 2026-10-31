package routing

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestDiscoverRootPage(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ": &fstest.MapFile{Data: []byte("templ Page() {}")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	r := routes[0]
	if r.Path != "/" {
		t.Errorf("Path = %q, want %q", r.Path, "/")
	}
	if r.SourceFile != "page.templ" {
		t.Errorf("SourceFile = %q, want %q", r.SourceFile, "page.templ")
	}
	if r.Mode != ModeSSR {
		t.Errorf("Mode = %q, want %q", r.Mode, ModeSSR)
	}
}

func TestDiscoverEmptyApp(t *testing.T) {
	fsys := fstest.MapFS{
		"about/card.templ": &fstest.MapFile{Data: []byte("templ Card() {}")},
	}
	if _, err := Discover(fsys); err == nil {
		t.Fatal("Discover succeeded with no page files, want error")
	}
}

// TestDiscoverAboutOnly matches the B03 acceptance fixture: an app with
// only about/page.templ (no root page) is a valid route tree.
func TestDiscoverAboutOnly(t *testing.T) {
	fsys := fstest.MapFS{
		"about/page.templ": &fstest.MapFile{Data: []byte("templ About() {}")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/about" || routes[0].SourceFile != "about/page.templ" {
		t.Errorf("routes = %+v, want single /about route", routes)
	}
}

func TestDiscoverNestedPagesSorted(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":                   &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":             &fstest.MapFile{Data: []byte("templ About() {}")},
		"deep/nested/route/page.templ": &fstest.MapFile{Data: []byte("templ Deep() {}")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	wantPaths := []string{"/", "/about", "/deep/nested/route"}
	wantSources := []string{"page.templ", "about/page.templ", "deep/nested/route/page.templ"}
	if len(routes) != len(wantPaths) {
		t.Fatalf("routes = %d, want %d; got %+v", len(routes), len(wantPaths), routes)
	}
	for i, r := range routes {
		if r.Path != wantPaths[i] {
			t.Errorf("routes[%d].Path = %q, want %q (all: %+v)", i, r.Path, wantPaths[i], routes)
		}
		if r.SourceFile != wantSources[i] {
			t.Errorf("routes[%d].SourceFile = %q, want %q", i, r.SourceFile, wantSources[i])
		}
		if r.Mode != ModeSSR {
			t.Errorf("routes[%d].Mode = %q, want ssr", i, r.Mode)
		}
	}
}

func TestDiscoverIgnoresNonPageFilesAtDepth(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":       &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/card.templ": &fstest.MapFile{Data: []byte("templ Card() {}")},
		"about/notes.txt":  &fstest.MapFile{Data: []byte("not a page")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/" {
		t.Errorf("routes = %+v, want only root route; components are not pages", routes)
	}
}

func TestDiscoverIgnoresNonRootPageFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":      &fstest.MapFile{Data: []byte("templ Page() {}")},
		"component.templ": &fstest.MapFile{Data: []byte("templ Component() {}")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 {
		t.Errorf("routes = %d, want only root route; got %+v", len(routes), routes)
	}
}

// TestDiscoverSvelteClassification matches the B04 acceptance fixture:
// dashboard/page.svelte => CSR mode.
func TestDiscoverSvelteClassification(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":            &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":      &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("routes = %d, want 3; got %+v", len(routes), routes)
	}
	var dash *Route
	for i := range routes {
		if routes[i].Path == "/dashboard" {
			dash = &routes[i]
		}
	}
	if dash == nil {
		t.Fatalf("/dashboard not discovered; got %+v", routes)
	}
	if dash.Mode != ModeCSR {
		t.Errorf("/dashboard Mode = %q, want csr", dash.Mode)
	}
	if dash.SourceFile != "dashboard/page.svelte" {
		t.Errorf("SourceFile = %q, want dashboard/page.svelte", dash.SourceFile)
	}
}

func TestDiscoverSvelteOnlyTree(t *testing.T) {
	fsys := fstest.MapFS{
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	r := routes[0]
	if r.Path != "/dashboard/settings" || r.Mode != ModeCSR {
		t.Errorf("route = %+v, want /dashboard/settings csr", r)
	}
}

func TestDiscoverIgnoresOtherSvelteFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":            &fstest.MapFile{Data: []byte("templ Page() {}")},
		"dashboard/card.svelte": &fstest.MapFile{Data: []byte("<div>card</div>")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/" {
		t.Errorf("routes = %+v, want only root route; .svelte components are not pages", routes)
	}
}

// TestDiscoverIgnoresNonPageComponents is the B05 acceptance test: no
// accidental routes from component files, exhaustively across both
// technologies, both file extensions, and nested directories.
func TestDiscoverIgnoresNonPageComponents(t *testing.T) {
	fsys := fstest.MapFS{
		// Valid root page so discovery succeeds.
		"page.templ": &fstest.MapFile{Data: []byte("templ Page() {}")},
		// Non-page .templ components at multiple depths.
		"card.templ":            &fstest.MapFile{Data: []byte("templ Card() {}")},
		"about/sidebar.templ":   &fstest.MapFile{Data: []byte("templ Sidebar() {}")},
		"deep/nested/nav.templ": &fstest.MapFile{Data: []byte("templ Nav() {}")},
		// Non-page .svelte components at multiple depths.
		"widget.svelte":                   &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/chart.svelte":          &fstest.MapFile{Data: []byte("<svg />")},
		"dashboard/settings/panel.svelte": &fstest.MapFile{Data: []byte("<aside />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want exactly 1 (the root page); got %+v", len(routes), routes)
	}
	if routes[0].Path != "/" || routes[0].SourceFile != "page.templ" {
		t.Errorf("route = %+v, want only the root page route", routes[0])
	}
}

// TestClassifyPageExactFilenames locks the exact-filename contract from
// binding decision #4: near-miss names must not classify as pages.
func TestClassifyPageExactFilenames(t *testing.T) {
	for base, want := range map[string]RenderMode{
		"page.templ":  ModeSSR,
		"page.svelte": ModeCSR,
	} {
		if got, ok := classifyPage(base); !ok || got != want {
			t.Errorf("classifyPage(%q) = (%q, %v), want (%q, true)", base, got, ok, want)
		}
	}
	// Every other name — including near-misses — is a component, not a page.
	for _, base := range []string{
		"Page.templ", "pages.templ", "page.htm", "page.js",
		"Page.svelte", "pages.svelte", "component.svelte", "layout.templ",
		"layout.svelte", "card.templ", ".svelte", "temple.templ",
	} {
		if mode, ok := classifyPage(base); ok {
			t.Errorf("classifyPage(%q) = (%q, true), want not-a-page", base, mode)
		}
	}
}

// errorFS always fails with a permission error, exercising Discover's
// walk error wrapping.
type errorFS struct{}

func (errorFS) Open(string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: "page.templ", Err: fs.ErrPermission}
}

func TestDiscoverErrorWrapped(t *testing.T) {
	_, e := Discover(errorFS{})
	if e == nil {
		t.Fatal("Discover on failing FS succeeded, want error")
	}
	var want *fs.PathError
	if !errors.As(e, &want) {
		t.Errorf("Discover error = %v, want it to wrap an fs.PathError", e)
	}
}
