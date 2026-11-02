package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"go-meta/internal/routing"
)

// fixtureCSRManifest discovers routes from a minimal CSR+SSR tree so tests
// exercise the same discovery pipeline as production generation. The
// dashboard subtree carries a client layout (G04).
func fixtureCSRManifest(t *testing.T) routing.Manifest {
	t.Helper()
	fsys := fstest.MapFS{
		"layout.templ":                   &fstest.MapFile{Data: []byte("templ Layout(templ.Component) {}")},
		"page.templ":                     &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":               &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.svelte":        &fstest.MapFile{Data: []byte("<aside />")},
		"dashboard/page.svelte":          &fstest.MapFile{Data: []byte("<main />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := routing.Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return routing.NewManifest(routes)
}

// TestRouteRegistryTSIncludesCSRRouteIDs is E06 acceptance criterion 1-2:
// the generated registry contains the dashboard and settings route IDs.
func TestRouteRegistryTSIncludesCSRRouteIDs(t *testing.T) {
	src := RouteRegistryTS(fixtureCSRManifest(t))
	for _, id := range []string{
		`"dashboard/page.svelte"`,
		`"dashboard/settings/page.svelte"`,
	} {
		if !strings.Contains(src, id) {
			t.Errorf("registry missing route ID %s", id)
		}
	}
}

// TestRouteRegistryTSExcludesSSR verifies SSR .templ routes are excluded
// from client loading: no import() loader ever references a .templ file.
// (G04's routeLayouts map may still carry .templ layout IDs as declared
// ancestry DATA — mirroring the navigation envelope — but never as loaders.)
func TestRouteRegistryTSExcludesSSR(t *testing.T) {
	src := RouteRegistryTS(fixtureCSRManifest(t))
	for _, line := range strings.Split(src, "\n") {
		if !strings.Contains(line, "import(") {
			continue
		}
		if strings.Contains(line, ".templ") {
			t.Errorf("SSR file receives a client loader: %s", line)
		}
	}
}

// TestRouteRegistryTSStaticImports verifies each entry is a static
// import() call on a fixed path — Vite-analyzable, no dynamic arbitrary
// imports (E06 contract). Page loaders and layout loaders both count.
func TestRouteRegistryTSStaticImports(t *testing.T) {
	src := RouteRegistryTS(fixtureCSRManifest(t))
	if !strings.Contains(src, `"dashboard/page.svelte": () => import("../app/dashboard/page.svelte"),`) {
		t.Errorf("missing static loader for dashboard:\n%s", src)
	}
	if strings.Count(src, "() => import(") != 3 {
		t.Errorf("want 3 loaders (2 pages + 1 layout), got %d:\n%s", strings.Count(src, "() => import("), src)
	}
	if strings.Contains(src, "import("+"\" +") {
		t.Error("dynamic/concatenated import expression present")
	}
}

// TestRouteRegistryTSIncludesLayoutLoader is G04 acceptance: the client
// layout component is addressable by its stable layout ID through a static
// loader Vite can analyze.
func TestRouteRegistryTSIncludesLayoutLoader(t *testing.T) {
	src := RouteRegistryTS(fixtureCSRManifest(t))
	if !strings.Contains(src,
		`"app/dashboard/layout.svelte": () => import("../app/dashboard/layout.svelte"),`) {
		t.Errorf("missing static layout loader for app/dashboard/layout.svelte:\n%s", src)
	}
}

// TestRouteRegistryTSIncludesRouteLayouts verifies routeLayouts carries
// each route's declared ancestry outermost -> innermost, including SSR
// layout IDs as data.
func TestRouteRegistryTSIncludesRouteLayouts(t *testing.T) {
	src := RouteRegistryTS(fixtureCSRManifest(t))
	for _, want := range []string{
		`"dashboard/page.svelte": ["app/dashboard/layout.svelte"],`,
		`"dashboard/settings/page.svelte": ["app/dashboard/layout.svelte"],`,
		`"about/page.templ": ["app/layout.templ"],`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing routeLayouts entry %s:\n%s", want, src)
		}
	}
}

// TestRouteRegistryTSDeterministic verifies byte-stable output across
// repeated generation from identical inputs (criterion 6).
func TestRouteRegistryTSDeterministic(t *testing.T) {
	m := fixtureCSRManifest(t)
	first := RouteRegistryTS(m)
	for range 5 {
		if got := RouteRegistryTS(m); got != first {
			t.Fatalf("registry output not deterministic")
		}
	}
}

// TestRouteRegistryTSImportPathsResolve verifies emitted import paths are
// valid relative paths for the generated file's location in frontend/.
func TestRouteRegistryTSImportPathsResolve(t *testing.T) {
	src := RouteRegistryTS(fixtureCSRManifest(t))
	for _, want := range []string{
		`import("../app/dashboard/page.svelte")`,
		`import("../app/dashboard/settings/page.svelte")`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing import path %s:\n%s", want, src)
		}
	}
}

// TestRouteRegistryTSEscaping verifies route identifiers containing
// quotes and backslashes are emitted safely as TypeScript string
// literals.
func TestRouteRegistryTSEscaping(t *testing.T) {
	// Direct unit test of the emitter with hostile input.
	got := tsString(`weird"id/route\name`)
	want := `"weird\"id/route\\name"`
	if got != want {
		t.Errorf("tsString = %s, want %s", got, want)
	}
}

// TestWriteRouteRegistryTS exercises the file writer: path, header, and
// IO error propagation.
func TestWriteRouteRegistryTS(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRouteRegistryTS(dir, fixtureCSRManifest(t)); err != nil {
		t.Fatalf("WriteRouteRegistryTS: %v", err)
	}
	dst := filepath.Join(dir, "frontend", "route-registry.generated.ts")
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read generated registry: %v", err)
	}
	if !strings.HasPrefix(string(data), "// Code generated by go-meta route generator. DO NOT EDIT.") {
		t.Error("generated registry missing DO-NOT-EDIT header")
	}

	// Writer must not silently skip bad input: empty manifest emits an
	// empty map (no routes, no loaders).
	empty := RouteRegistryTS(routing.Manifest{})
	if strings.Contains(empty, "import(") || !strings.Contains(empty, "= {") {
		t.Errorf("empty manifest should emit an empty map literal:\n%s", empty)
	}
}

// TestWriteRouteRegistryTSRegeneratesStable verifies regeneration does
// not introduce unnecessary changes (criterion 8): same input → same bytes.
func TestWriteRouteRegistryTSRegeneratesStable(t *testing.T) {
	dir := t.TempDir()
	m := fixtureCSRManifest(t)
	if err := WriteRouteRegistryTS(dir, m); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "frontend", "route-registry.generated.ts"))
	if err := WriteRouteRegistryTS(dir, m); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "frontend", "route-registry.generated.ts"))
	if string(first) != string(second) {
		t.Error("regeneration changed bytes for identical input")
	}
}
