package routing

import (
	"errors"
	"io/fs"
	"strings"
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

// TestDiscoverRootLayoutAncestry matches the B06 acceptance fixture: root
// layout.templ is reported by both / and /about.
func TestDiscoverRootLayoutAncestry(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":     &fstest.MapFile{Data: []byte("templ Layout() {}")},
		"page.templ":       &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ": &fstest.MapFile{Data: []byte("templ About() {}")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2; got %+v", len(routes), routes)
	}
	for _, r := range routes {
		if len(r.Layouts) != 1 {
			t.Errorf("route %q Layouts = %+v, want one root layout", r.Path, r.Layouts)
			continue
		}
		l := r.Layouts[0]
		if l.SourceFile != "layout.templ" || l.Mode != ModeSSR || l.ID != "layout.templ" {
			t.Errorf("route %q layout = %+v, want layout.templ ssr", r.Path, l)
		}
	}
}

func TestDiscoverLayoutNotARoute(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ": &fstest.MapFile{Data: []byte("templ Layout() {}")},
		"page.templ":   &fstest.MapFile{Data: []byte("templ Page() {}")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/" {
		t.Errorf("routes = %+v, want only the root page; layouts create no URL", routes)
	}
}

// TestDiscoverNestedTemplLayoutChain verifies ancestry walks intermediate
// directories: docs/layout.templ wraps docs/page.templ plus the root layout.
func TestDiscoverNestedTemplLayoutChain(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":      &fstest.MapFile{Data: []byte("templ Root() {}")},
		"page.templ":        &fstest.MapFile{Data: []byte("templ Page() {}")},
		"docs/layout.templ": &fstest.MapFile{Data: []byte("templ Docs() {}")},
		"docs/page.templ":   &fstest.MapFile{Data: []byte("templ DocPage() {}")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var docs *Route
	for i := range routes {
		if routes[i].Path == "/docs" {
			docs = &routes[i]
		}
	}
	if docs == nil {
		t.Fatalf("/docs not discovered: %+v", routes)
	}
	if len(docs.Layouts) != 2 {
		t.Fatalf("/docs Layouts = %+v, want [root, docs] chain", docs.Layouts)
	}
	if docs.Layouts[0].SourceFile != "layout.templ" || docs.Layouts[1].SourceFile != "docs/layout.templ" {
		t.Errorf("/docs Layouts = %+v, want outermost-first [layout.templ, docs/layout.templ]", docs.Layouts)
	}
}

// TestDiscoverConflictingLayoutsError covers the same-directory
// layout.templ + layout.svelte rejection (blueprint §3.4).
func TestDiscoverConflictingLayoutsError(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":  &fstest.MapFile{Data: []byte("templ T() {}")},
		"layout.svelte": &fstest.MapFile{Data: []byte("<div />")},
		"page.templ":    &fstest.MapFile{Data: []byte("templ Page() {}")},
	}
	_, err := Discover(fsys)
	if err == nil {
		t.Fatal("Discover succeeded with layout.templ + layout.svelte in the root, want error")
	}
	want := `conflicting layouts`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to mention %q", err.Error(), want)
	}
}

// TestDiscoverRootSvelteLayout matches the blueprint's target app tree:
// dashboard/layout.svelte wraps both dashboard CSR pages.
func TestDiscoverNestedSvelteLayoutAncestry(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":                   &fstest.MapFile{Data: []byte("templ Root() {}")},
		"page.templ":                     &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":               &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.svelte":        &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/page.svelte":          &fstest.MapFile{Data: []byte("<main />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	byPath := map[string]Route{}
	for _, r := range routes {
		byPath[r.Path] = r
	}
	dash, ok := byPath["/dashboard"]
	if !ok {
		t.Fatalf("/dashboard missing: %+v", routes)
	}
	settings, ok := byPath["/dashboard/settings"]
	if !ok {
		t.Fatalf("/dashboard/settings missing: %+v", routes)
	}
	for name, r := range map[string]Route{"dashboard": dash, "settings": settings} {
		if len(r.Layouts) != 2 {
			t.Fatalf("/%s Layouts = %+v, want [layout.templ, dashboard/layout.svelte]", name, r.Layouts)
		}
		if r.Layouts[0].SourceFile != "layout.templ" || r.Layouts[0].Mode != ModeSSR {
			t.Errorf("/%s outermost = %+v, want layout.templ ssr", name, r.Layouts[0])
		}
		if r.Layouts[1].SourceFile != "dashboard/layout.svelte" || r.Layouts[1].Mode != ModeCSR {
			t.Errorf("/%s inner = %+v, want dashboard/layout.svelte csr", name, r.Layouts[1])
		}
		// Both dashboard routes report the same nested layout ID (B07 acceptance).
		if r.Layouts[1].ID != "dashboard/layout.svelte" {
			t.Errorf("/%s layout ID = %q, want dashboard/layout.svelte", name, r.Layouts[1].ID)
		}
	}
}

func TestDiscoverRootSvelteLayoutWithCSRPage(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.svelte": &fstest.MapFile{Data: []byte("<div />")},
		"page.svelte":   &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %+v, want single / route", routes)
	}
	r := routes[0]
	if len(r.Layouts) != 1 || r.Layouts[0].SourceFile != "layout.svelte" || r.Layouts[0].Mode != ModeCSR {
		t.Errorf("Layouts = %+v, want [layout.svelte csr]", r.Layouts)
	}
}

// TestDiscoverSiblingLayoutNoLeak verifies a layout applies only within its
// subtree: dashboard/layout.svelte must not wrap /about.
func TestDiscoverSiblingLayoutNoLeak(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":              &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":        &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.svelte": &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/page.svelte":   &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, r := range routes {
		switch r.Path {
		case "/", "/about":
			for _, l := range r.Layouts {
				if l.SourceFile == "dashboard/layout.svelte" {
					t.Errorf("route %q wrapped by sibling layout %q", r.Path, l.SourceFile)
				}
			}
		case "/dashboard":
			if len(r.Layouts) != 1 || r.Layouts[0].SourceFile != "dashboard/layout.svelte" {
				t.Errorf("/dashboard Layouts = %+v, want dashboard/layout.svelte only", r.Layouts)
			}
		}
	}
}

func TestDiscoverSvelteLayoutAloneNotARoute(t *testing.T) {
	fsys := fstest.MapFS{
		"dashboard/layout.svelte":        &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/dashboard/settings" {
		t.Fatalf("routes = %+v, want only /dashboard/settings", routes)
	}
}

// TestDiscoverLayoutDoesNotWrapUnrelatedTree checks a layout applies only to
// its own subtree: a dashboard-only layout must not leak into /about.
func TestDiscoverLayoutScoping(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":             &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":       &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.templ": &fstest.MapFile{Data: []byte("templ Dash() {}")},
		"dashboard/page.svelte":  &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, r := range routes {
		switch r.Path {
		case "/", "/about":
			if len(r.Layouts) != 0 {
				t.Errorf("route %q unexpectedly wrapped by %+v", r.Path, r.Layouts)
			}
		case "/dashboard":
			// B06 classifies only layout.templ; dashboard/layout.templ is
			// in a CSR subtree, which later tasks (B09/G10) will reject.
			// Here we assert discovery stays silent-but-scoped for now.
		}
	}
}

// TestDiscoverDuplicatePageFiles is the B08 acceptance fixture: page.templ
// and page.svelte in the same folder fail discovery with both paths.
func TestDiscoverDuplicatePageFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":       &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ": &fstest.MapFile{Data: []byte("templ About() {}")},
	}
	fsys["about/page.svelte"] = &fstest.MapFile{Data: []byte("<main />")}

	_, err := Discover(fsys)
	if err == nil {
		t.Fatal("Discover succeeded with page.templ + page.svelte in about/, want error")
	}
	for _, want := range []string{`"/about"`, "about/page.templ", "about/page.svelte"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestDiscoverDuplicatePageFilesAtRoot(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":  &fstest.MapFile{Data: []byte("templ Page() {}")},
		"page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	_, err := Discover(fsys)
	if err == nil {
		t.Fatal("Discover succeeded with page.templ + page.svelte at root, want error")
	}
	for _, want := range []string{`"/"`, "page.templ", "page.svelte"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestValidateRoutesNoDuplicateOnDistinctPaths is a direct unit check that
// distinct routes validate cleanly.
func TestValidateRoutesNoDuplicateOnDistinctPaths(t *testing.T) {
	r1, _ := NewRoute("/", "page.templ", ModeSSR, nil)
	r2, _ := NewRoute("/dashboard", "dashboard/page.svelte", ModeCSR, nil)
	if err := ValidateRoutes([]Route{r1, r2}); err != nil {
		t.Errorf("ValidateRoutes on distinct routes = %v, want nil", err)
	}
}

// TestDiscoverSSRLayoutUnderCSRLayoutRejected matches the B09 rule: an SSR
// layout nested beneath a CSR layout is an unsupported mixed-mode chain.
func TestDiscoverSSRLayoutUnderCSRLayoutRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":                      &fstest.MapFile{Data: []byte("templ Page() {}")},
		"dashboard/layout.svelte":         &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/settings/layout.templ": &fstest.MapFile{Data: []byte("templ S() {}")},
		"dashboard/settings/page.svelte":  &fstest.MapFile{Data: []byte("<main />")},
	}
	_, err := Discover(fsys)
	if err == nil {
		t.Fatal("Discover succeeded with SSR layout beneath CSR layout, want error")
	}
	for _, want := range []string{"settings/layout.templ", "layout.svelte", "SSR layout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestDiscoverSSRPageUnderCSRLayoutRejected covers the page-side case of
// the same rule: page.templ directly inside a layout.svelte directory.
func TestDiscoverSSRPageUnderCSRLayoutRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":              &fstest.MapFile{Data: []byte("templ Page() {}")},
		"dashboard/layout.svelte": &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/page.templ":    &fstest.MapFile{Data: []byte("templ Dash() {}")},
	}
	_, err := Discover(fsys)
	if err == nil {
		t.Fatal("Discover succeeded with SSR page beneath CSR layout, want error")
	}
	for _, want := range []string{"dashboard/page.templ", "dashboard/layout.svelte"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestDiscoverRootSSRWrappingCSLSupported locks the supported mixed-mode
// direction (critical architecture requirement): layout.templ above
// layout.svelte and CSR pages is valid.
func TestDiscoverRootSSRWrappingCSLSupported(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":                   &fstest.MapFile{Data: []byte("templ Root() {}")},
		"page.templ":                     &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":               &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.svelte":        &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/page.svelte":          &fstest.MapFile{Data: []byte("<main />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover on the blueprint's target app tree failed: %v", err)
	}
	if len(routes) != 4 {
		t.Fatalf("routes = %d, want 4; got %+v", len(routes), routes)
	}
	var dash *Route
	for i := range routes {
		if routes[i].Path == "/dashboard" {
			dash = &routes[i]
		}
	}
	if dash == nil {
		t.Fatal("/dashboard missing")
	}
	if len(dash.Layouts) != 2 || dash.Layouts[0].Mode != ModeSSR || dash.Layouts[1].Mode != ModeCSR {
		t.Errorf("/dashboard Layouts = %+v, want [ssr root, csr dashboard]", dash.Layouts)
	}
}

// TestValidateRoutesPureCSRTree validates a fully client-side tree passes.
func TestValidateRoutesPureCSRTree(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.svelte":         &fstest.MapFile{Data: []byte("<div />")},
		"page.svelte":           &fstest.MapFile{Data: []byte("<main />")},
		"shop/layout.svelte":    &fstest.MapFile{Data: []byte("<div />")},
		"shop/cart/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	if _, err := Discover(fsys); err != nil {
		t.Errorf("Discover on pure CSR tree failed: %v", err)
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
