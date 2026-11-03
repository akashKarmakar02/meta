package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// B01: malformed bracket routes must be rejected during discovery, before
// any generated output exists. Each case names the declaration form and the
// rule it violates.
func TestValidateSegmentRejectsMalformedBrackets(t *testing.T) {
	cases := []struct {
		seg   string
		match string
	}{
		{"[]", "empty parameter name"},
		{"[...slug", "fully wrapped in brackets"},
		{"[...]", "empty parameter name"},
		{"[....slug]", "nested or dotted brackets"},
		{"[...slug]extra", "fully wrapped in brackets"},
		{"[[...slug]]", "nested brackets"},
		{"[...my slug]", "letters, digits, and underscores"},
		{"[..slug]", "must start with a letter or underscore"},
		{"[id", "fully wrapped in brackets"},
		{"id]", "fully wrapped in brackets"},
		{"[[id]", "nested brackets"},
		{"[id]]", "nested brackets"},
		{"[[id]]", "nested brackets"},
		{"[a]b", "fully wrapped in brackets"},
		{"a[b]", "fully wrapped in brackets"},
		{"[my slug]", "letters, digits, and underscores"},
		{"[my-slug]", "letters, digits, and underscores"},
		{"[1id]", "must start with a letter or underscore"},
		{"[id!]", "letters, digits, and underscores"},
	}
	for _, tc := range cases {
		t.Run(tc.seg, func(t *testing.T) {
			err := validateSegment(tc.seg, "app/"+tc.seg)
			if err == nil {
				t.Fatalf("validateSegment(%q) accepted a malformed segment", tc.seg)
			}
			assertErrContains(t, err, "INVALID_SEGMENT")
			assertErrContains(t, err, tc.match)
			assertErrContains(t, err, tc.seg)
		})
	}
}

// B01: well-formed declarations must pass validation unchanged. Valid
// existing dynamic routes (the Phase 1 app uses app/products/[id]) are the
// compatibility guarantee for this gate.
func TestValidateSegmentAcceptsValidSegments(t *testing.T) {
	for _, seg := range []string{"about", "products", "[id]", "[slug]", "[_private]", "[userId42]", "page", "_components", "[...slug]", "[...path]", "[...a1]"} {
		if err := validateSegment(seg, "app/"+seg); err != nil {
			t.Fatalf("validateSegment(%q) rejected a valid segment: %v", seg, err)
		}
	}
}

// B01: discovery walks through valid dynamic directories and still produces
// the documented route shape; valid Phase 1 routes survive hardening.
func TestDiscoverAcceptsValidDynamicRoute(t *testing.T) {
	fsys := fstest.MapFS{
		"products/[id]/page.svelte": {},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover with valid dynamic route: unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	assertRoute(t, routes[0], "/products/:id", ModeCSR)
}

// B01: each malformed declaration form must fail Discover at the walk, before
// generation output can be produced.
func TestDiscoverRejectsMalformedBracketRoutes(t *testing.T) {
	cases := []struct {
		name string
		seg  string
	}{
		{"empty name", "[]"},
		{"catch-all empty name", "[...]"},
		{"catch-all unbalanced", "[...slug"},
		{"unbalanced open", "[id"},
		{"unbalanced close", "id]"},
		{"nested brackets", "[[id]]"},
		{"bad param name", "[item-id]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			app := filepath.Join(dir, "app")
			write(t, app, filepath.Join("items", tc.seg, "page.svelte"))
			routes, err := Discover(os.DirFS(app))
			if err == nil {
				t.Fatalf("Discover accepted malformed route dir %q (routes: %+v)", tc.seg, routes)
			}
			assertErrContains(t, err, "INVALID_SEGMENT")
		})
	}
}

// B01: underscore-prefixed directories are colocated component folders, not
// route trees; their pages are ignored entirely.
func TestDiscoverSkipsUnderscoreDirectories(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	write(t, app, "dashboard/page.svelte")
	write(t, app, "_components/page.svelte") // would be a route if walked
	write(t, app, "shop/page.svelte")
	write(t, app, "shop/_lib/page.svelte")       // nested skip at any depth
	write(t, app, "shop/_partial/layout.svelte") // layouts skipped too

	routes, err := Discover(os.DirFS(app))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes (dashboard + shop), got %d: %+v", len(routes), routes)
	}
	assertRoute(t, routes[0], "/dashboard", ModeCSR)
	assertRoute(t, routes[1], "/shop", ModeCSR)
}

// B01: dot-directories (Vite cache, git metadata) are tool state, not routes.
func TestDiscoverSkipsDotDirectories(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	write(t, app, "about/page.svelte")
	write(t, app, ".vite/page.svelte")
	write(t, app, "shop/page.svelte")
	write(t, app, "shop/.cache/page.svelte")

	routes, err := Discover(os.DirFS(app))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes (about + shop), got %d: %+v", len(routes), routes)
	}
	assertRoute(t, routes[0], "/about", ModeCSR)
	assertRoute(t, routes[1], "/shop", ModeCSR)
}

// B01: a static sibling of a dynamic segment keeps both routes today; B06
// owns precedence redesign. This test locks current deterministic behavior
// so that change happens consciously.
func TestDiscoverStaticSiblingOfDynamicKeepsBoth(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	write(t, app, "products/new/page.svelte")
	write(t, app, "products/[id]/page.svelte")

	routes, err := Discover(os.DirFS(app))
	if err != nil {
		t.Fatalf("Discover: unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected both routes preserved, got %d: %+v", len(routes), routes)
	}
	if routes[0].Path != "/products/new" || routes[1].Path != "/products/:id" {
		t.Fatalf("static-before-dynamic order broke: %+v", routes)
	}
}

// B01: two dynamic siblings differing only in param name ([id] vs [slug]) are
// equivalent conflicting routes — deterministic rejection naming both sources.
func TestDiscoverRejectsEquivalentDynamicSiblings(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	write(t, app, "products/[id]/page.svelte")
	write(t, app, "products/[slug]/page.svelte")

	_, err := Discover(os.DirFS(app))
	if err == nil {
		t.Fatal("Discover accepted equivalent conflicting dynamic siblings")
	}
	assertErrContains(t, err, "ambiguous dynamic segments")
	assertErrContains(t, err, "[id]")
	assertErrContains(t, err, "[slug]")
	// Deterministic rejection names both source files so the developer can fix either.
	assertErrContains(t, err, "products/[id]/page.svelte")
	assertErrContains(t, err, "products/[slug]/page.svelte")
}

// B01: equivalent conflict detection must be deterministic — identical trees
// produce identical error text (both source files named).
func TestDynamicAmbiguityErrorIsDeterministic(t *testing.T) {
	build := func() string {
		dir := t.TempDir()
		app := filepath.Join(dir, "app")
		write(t, app, "products/[id]/page.svelte")
		write(t, app, "products/[slug]/page.svelte")
		_, err := Discover(os.DirFS(app))
		if err == nil {
			t.Fatal("expected ambiguity error")
		}
		return err.Error()
	}
	first := build()
	second := build() // fresh tree, same content
	if first != second {
		t.Fatalf("ambiguity error differs across identical trees:\n first: %s\nsecond: %s", first, second)
	}
}

// B01: page.static.templ is reserved for future static generation; it must be
// rejected loudly rather than silently ignored or registered.
func TestDiscoverRejectsReservedStaticFilename(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	write(t, app, "about/page.static.templ")

	_, err := Discover(os.DirFS(app))
	if err == nil {
		t.Fatal("Discover accepted reserved filename page.static.templ")
	}
	assertErrContains(t, err, "STATIC_NOT_IMPLEMENTED")
}

func TestDiscoverSSRInsideDynamicDir(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	write(t, app, "items/[id]/page.templ")

	_, err := Discover(os.DirFS(app))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
}

func assertErrContains(t *testing.T, err error, substr string) {
	t.Helper()
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not contain %q", err.Error(), substr)
	}
}

func assertRoute(t *testing.T, r Route, wantPath string, wantMode RenderMode) {
	t.Helper()
	if r.Path != wantPath {
		t.Errorf("route path = %q, want %q", r.Path, wantPath)
	}
	if r.Mode != wantMode {
		t.Errorf("route %q mode = %q, want %q", wantPath, r.Mode, wantMode)
	}
}

func write(t *testing.T, app string, rel string) {
	t.Helper()
	full := filepath.Join(app, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte("<div>fixture</div>\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
