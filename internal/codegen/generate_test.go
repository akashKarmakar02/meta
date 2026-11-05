package codegen

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/akashKarmakar02/meta/internal/routing"
)

func fixtureManifest(t *testing.T) routing.Manifest {
	t.Helper()
	fsys := fstest.MapFS{
		"layout.templ":                   &fstest.MapFile{Data: []byte("templ Layout(templ.Component) {}")},
		"page.templ":                     &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":               &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.svelte":        &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/page.svelte":          &fstest.MapFile{Data: []byte("<main />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return routing.NewManifest(routes)
}

// TestVerifySymbolsRejection is the C09 acceptance criterion: an SSR page
// whose templ component does not match the expected symbol fails generation
// with an error naming the source file, before any code is emitted.
func TestVerifySymbolsRejection(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":          &fstest.MapFile{Data: []byte("templ Layout(templ.Component) {}")},
		"page.templ":            &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":      &fstest.MapFile{Data: []byte("templ Hello() {}")}, // wrong name
		"dashboard/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	m := routing.NewManifest(routes)

	err = VerifySymbols(fsys, m)
	if err == nil {
		t.Fatal("VerifySymbols: want error for misnamed component, got nil")
	}
	for _, want := range []string{"about/page.templ", "About"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// B03: a catch-all route emits its Fiber "+" path and the declared catch-all
// name in the generated Go literal, so WithRouteParams can decode the value
// under the right key.
func TestRoutesFileCatchAllLiteral(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":                             {Data: []byte("templ Layout(templ.Component) {}")},
		"docs/[...slug]/page.templ":                {Data: []byte("templ Page() {}")},
		"orgs/[orgId]/files/[...path]/page.svelte": {Data: []byte("<main />")},
	}
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	m := routing.NewManifest(routes)
	src, err := RoutesFile(m)
	if err != nil {
		t.Fatalf("RoutesFile: %v", err)
	}
	s := string(src)
	for _, want := range []string{
		`Path: "/docs/+"`,
		`CatchAll: "slug"`,
		`Path: "/orgs/:orgId/files/+"`,
		`CatchAll: "path"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("generated source missing %q:\n%s", want, s)
		}
	}
	// The generated file must still parse.
	if _, err := parser.ParseFile(token.NewFileSet(), "routes_gen.go", s, parser.ParseComments); err != nil {
		t.Errorf("generated source does not parse: %v", err)
	}
}

func TestCSRPageWithNearestTemplLayout(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ":                   {Data: []byte("templ Layout(templ.Component) {}")},
		"dashboard/layout.templ":         {Data: []byte("templ DashboardLayout(templ.Component) {}")},
		"dashboard/page.svelte":          {Data: []byte("<main />")},
		"dashboard/settings/page.svelte": {Data: []byte("<main />")},
	}
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	manifest := routing.NewManifest(routes)
	if err := VerifySymbols(fsys, manifest); err != nil {
		t.Fatal(err)
	}
	goSource, err := RoutesFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	tsSource := RouteRegistryTS(manifest)
	for _, route := range routes {
		if len(route.Layouts) != 1 || route.Layouts[0].ID != "app/dashboard/layout.templ" {
			t.Fatalf("%s: layouts = %+v", route.Path, route.Layouts)
		}
		for _, source := range []string{string(goSource), tsSource} {
			if !strings.Contains(source, route.ID) || !strings.Contains(source, "app/dashboard/layout.templ") {
				t.Errorf("generated source lacks route and layout identifiers: %s", source)
			}
		}
	}
	if !strings.Contains(string(goSource), "DashboardLayout(content)") {
		t.Fatalf("CSR layout binding missing: %s", goSource)
	}
}

// TestVerifySymbolsAccepts: a valid tree verifies cleanly, and a CSR-only
// tree (no SSR routes at all) also verifies cleanly — no nonexistent SSR
// component imports are demanded.
func TestVerifySymbolsAccepts(t *testing.T) {
	valid := fstest.MapFS{
		"layout.templ":          &fstest.MapFile{Data: []byte("templ Layout(templ.Component) {}")},
		"page.templ":            &fstest.MapFile{Data: []byte("templ Page() {}")},
		"dashboard/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := routing.Discover(valid)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := VerifySymbols(valid, routing.NewManifest(routes)); err != nil {
		t.Fatalf("VerifySymbols valid tree: %v", err)
	}

	csrOnly := fstest.MapFS{
		"dashboard/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err = routing.Discover(csrOnly)
	if err != nil {
		t.Fatalf("Discover csr-only: %v", err)
	}
	if err := VerifySymbols(csrOnly, routing.NewManifest(routes)); err != nil {
		t.Fatalf("VerifySymbols CSR-only tree: %v", err)
	}
}

// TestPageSymbol locks the source-path-to-symbol convention used by the
// generated SSRPage mapping.
func TestPageSymbol(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"page.templ", "Page"},
		{"about/page.templ", "About"},
		{"dashboard/settings/page.svelte", "DashboardSettings"},
	}
	for _, tc := range cases {
		r := routing.Route{SourceFile: tc.src}
		if got := pageSymbol(r); got != tc.want {
			t.Errorf("pageSymbol(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestRoutesFileSSRPageMapping: each SSR route maps to its own compiled
// symbol from its own package alias; CSR routes never appear there.
func TestRoutesFileSSRPageMapping(t *testing.T) {
	srcB, err := RoutesFile(fixtureManifest(t))
	if err != nil {
		t.Fatalf("RoutesFile: %v", err)
	}
	src := string(srcB)
	for _, want := range []string{
		`if r.ID == "page.templ" {`,
		"return layout0.Layout(page0.Page())",
		`if r.ID == "about/page.templ" {`,
		"return layout0.Layout(page1.About())",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source missing %q:\n%s", want, src)
		}
	}
	if strings.Contains(src, "Dashboard") && strings.Contains(src, "page2.") {
		t.Error("CSR route leaked into SSRPage mapping")
	}
}

// TestRoutesFileParses is the C02 acceptance criterion: the generated file
// parses with go/parser.
func TestRoutesFileParses(t *testing.T) {
	src, err := RoutesFile(fixtureManifest(t))
	if err != nil {
		t.Fatalf("RoutesFile: %v", err)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "routes_gen.go", src, parser.ParseComments); err != nil {
		t.Fatalf("generated file does not parse: %v\n%s", err, src)
	}
}

func TestRoutesFileContent(t *testing.T) {
	srcB, err2 := RoutesFile(fixtureManifest(t))
	if err2 != nil {
		t.Fatalf("RoutesFile: %v", err2)
	}
	src := string(srcB)
	for _, want := range []string{
		"Code generated by go-meta route generator. DO NOT EDIT.",
		"package routes",
		"func RegisterRoutes() []routing.Route {",
		`{ID: "page.templ", Path: "/", SourceFile: "page.templ", Mode: "ssr"`,
		`{ID: "dashboard/page.svelte", Path: "/dashboard", SourceFile: "dashboard/page.svelte", Mode: "csr"`,
		`{ID: "app/layout.templ", SourceFile: "layout.templ", Mode: "ssr"}`,
		`{ID: "app/dashboard/layout.svelte", SourceFile: "dashboard/layout.svelte", Mode: "csr"}`,
		// C05: per-route symbol mapping with deterministic aliases.
		"page0 \"github.com/akashKarmakar02/meta/app\"",
		"page1 \"github.com/akashKarmakar02/meta/app/about\"",
		"templ.Component",
		// D06: layouts compose around pages at generation time, innermost
		// wrap first so the final nesting matches the manifest's
		// outermost-first chain order.
		"layout0 \"github.com/akashKarmakar02/meta/app\"",
		"return layout0.Layout(page0.Page())",
		"return layout0.Layout(page1.About())",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source missing %q:\n%s", want, src)
		}
	}
	// Routes in path-sorted order.
	rootIdx := strings.Index(src, `Path: "/"`)
	aboutIdx := strings.Index(src, `Path: "/about"`)
	dashIdx := strings.Index(src, `Path: "/dashboard"`)
	if !(rootIdx < aboutIdx && aboutIdx < dashIdx) {
		t.Error("routes not emitted in manifest (path-sorted) order")
	}
}

// TestRoutesFileDeterministic: identical inputs generate identical bytes,
// repeated.
func TestRoutesFileDeterministic(t *testing.T) {
	m := fixtureManifest(t)
	wantB, _ := RoutesFile(m)
	want := string(wantB)
	for i := 0; i < 10; i++ {
		gotB, _ := RoutesFile(fixtureManifest(t))
		if got := string(gotB); got != want {
			t.Fatalf("iteration %d differs:\ngot:  %s\nwant: %s", i, got, want)
		}
	}
}

func TestWriteRoutesFile(t *testing.T) {
	dir := t.TempDir()
	m := fixtureManifest(t)
	if err := WriteRoutesFile(dir, m); err != nil {
		t.Fatalf("WriteRoutesFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "generated", "routes_gen.go"))
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	wantC, _ := RoutesFile(m)
	if string(data) != string(wantC) {
		t.Error("written file differs from RoutesFile output")
	}
}

// TestWriteRoutesFileIOError verifies write failures propagate: a file
// blocking the generated/ directory must surface an actionable error.
func TestWriteRoutesFileIOError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "generated")
	if err := os.WriteFile(blocker, []byte("occupied"), 0o644); err != nil {
		t.Fatalf("setup blocker: %v", err)
	}
	err := WriteRoutesFile(dir, fixtureManifest(t))
	if err == nil {
		t.Fatal("WriteRoutesFile succeeded despite blocked path, want error")
	}
	if !strings.Contains(err.Error(), "codegen:") {
		t.Errorf("error = %v, want it prefixed with codegen:", err)
	}
}

// TestWriteRoutesFileNoopPreservesMTime (C06): regenerating with unchanged
// inputs must not touch the output file — neither bytes nor mtime. A rewrite
// would bump the mtime and trigger watcher rebuild loops (Air/Vite) even
// though nothing changed.
func TestWriteRoutesFileNoopPreservesMTime(t *testing.T) {
	dir := t.TempDir()
	m := fixtureManifest(t)
	if err := WriteRoutesFile(dir, m); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	dst := filepath.Join(dir, "generated", "routes_gen.go")
	before, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure the no-op check sees a genuinely different mtime if it rewrote:
	// backdate the file slightly so any rewrite is detectable by mtime.
	past := before.ModTime().Add(-2 * time.Second)
	if err := os.Chtimes(dst, past, past); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := WriteRoutesFile(dir, m); err != nil {
			t.Fatalf("no-op write %d: %v", i, err)
		}
	}
	after, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(past) {
		t.Fatalf("no-op generation changed the file mtime: %v -> %v", past, after.ModTime())
	}
}
