package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// B03: a terminal "[...name]" segment normalizes to Fiber's bare "+" catch-all
// and records the declared name on Route.CatchAll.
func TestDiscoverCatchAllNormalization(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/[...slug]/page.templ": {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("route count = %d, want 1 (%+v)", len(routes), routes)
	}
	r := routes[0]
	if r.Path != "/docs/+" {
		t.Errorf("Path = %q, want %q", r.Path, "/docs/+")
	}
	if r.CatchAll != "slug" {
		t.Errorf("CatchAll = %q, want %q", r.CatchAll, "slug")
	}
	if r.SourceFile != "docs/[...slug]/page.templ" {
		t.Errorf("SourceFile = %q", r.SourceFile)
	}
	if r.Mode != ModeSSR {
		t.Errorf("Mode = %q, want ssr", r.Mode)
	}
}

// B03: a catch-all may follow named single-segment parameters; the declared
// catch-all name is recorded and the named params remain in the path.
func TestDiscoverCatchAllAfterParams(t *testing.T) {
	fsys := fstest.MapFS{
		"organizations/[orgId]/files/[...path]/page.svelte": {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("route count = %d, want 1 (%+v)", len(routes), routes)
	}
	r := routes[0]
	if r.Path != "/organizations/:orgId/files/+" {
		t.Errorf("Path = %q, want %q", r.Path, "/organizations/:orgId/files/+")
	}
	if r.CatchAll != "path" {
		t.Errorf("CatchAll = %q, want %q", r.CatchAll, "path")
	}
}

// B03: a catch-all must be the last path segment. Non-terminal declarations
// are rejected during the walk, before generated output exists.
func TestDiscoverRejectsNonTerminalCatchAll(t *testing.T) {
	for _, dir := range []string{
		"docs/[...slug]/extra/page.svelte",
		"docs/[...slug]/[id]/page.svelte",
		"docs/[...slug]/a/b/page.svelte",
	} {
		t.Run(dir, func(t *testing.T) {
			fsys := fstest.MapFS{dir: {Data: []byte("x")}}
			_, err := Discover(fsys)
			if err == nil {
				t.Fatalf("Discover accepted non-terminal catch-all %q", dir)
			}
			assertErrContains(t, err, "INVALID_SEGMENT")
			assertErrContains(t, err, "last path segment")
		})
	}
}

// B03: the catch-all segment itself must be a well-formed "[...name]".
func TestDiscoverRejectsMalformedCatchAll(t *testing.T) {
	for _, dir := range []string{
		"docs/[...]/page.svelte",        // empty name
		"docs/[...slug/page.svelte",     // unbalanced
		"docs/[....slug]/page.svelte",   // dotted
		"docs/[...slug]x/page.svelte",   // trailing junk
		"docs/[...my slug]/page.svelte", // bad name
	} {
		t.Run(dir, func(t *testing.T) {
			fsys := fstest.MapFS{dir: {Data: []byte("x")}}
			_, err := Discover(fsys)
			if err == nil {
				t.Fatalf("Discover accepted malformed catch-all %q", dir)
			}
			assertErrContains(t, err, "INVALID_SEGMENT")
		})
	}
}

// B03: a catch-all directory is a dynamic directory — Go source inside it is
// rejected, matching the single-segment dynamic rule.
func TestGoSourceInsideCatchAllDirectoryRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/[...slug]/page.templ": {Data: []byte("x")},
		"docs/[...slug]/helpers.go": {Data: []byte("x")},
	}
	_, err := Discover(fsys)
	if err == nil || !strings.Contains(err.Error(), "helpers.go") {
		t.Fatalf("expected Go-source-in-catch-all error, got %v", err)
	}
}

// B03: a catch-all route and a static route under its prefix both survive
// discovery; the static path sorts (registers) before the catch-all so Fiber
// matches the static path first.
func TestCatchAllAndStaticCoexistStaticFirst(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/[...slug]/page.svelte": {Data: []byte("x")},
		"docs/intro/page.svelte":     {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	idx := map[string]int{}
	for i, r := range routes {
		idx[r.Path] = i
	}
	staticIdx, catchIdx := idx["/docs/intro"], idx["/docs/+"]
	if staticIdx == -1 || catchIdx == -1 {
		t.Fatalf("missing routes: %+v", routes)
	}
	if staticIdx > catchIdx {
		t.Errorf("static /docs/intro (index %d) must register before catch-all /docs/+ (index %d)", staticIdx, catchIdx)
	}
}

// B03: a single-segment dynamic route directly under a catch-all prefix is the
// documented one-segment overlap and is allowed; it registers before the
// catch-all.
func TestCatchAllAndSingleSegmentDynamicAllowed(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/[...slug]/page.svelte": {Data: []byte("x")},
		"docs/[id]/page.svelte":      {Data: []byte("x")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover rejected a valid one-segment overlap: %v", err)
	}
	idx := map[string]int{}
	for i, r := range routes {
		idx[r.Path] = i
	}
	if idx["/docs/:id"] > idx["/docs/+"] {
		t.Errorf("dynamic /docs/:id must register before catch-all /docs/+")
	}
}

// B03: catch-all conflicts that Fiber cannot resolve deterministically are
// rejected, naming both routes.
func TestCatchAllConflictsRejected(t *testing.T) {
	cases := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"nested catch-all", fstest.MapFS{
			"docs/[...slug]/page.svelte":       {},
			"docs/guide/[...page]/page.svelte": {},
		}, "ambiguous catch-all"},
		{"multi-segment dynamic under catch-all", fstest.MapFS{
			"docs/[...slug]/page.svelte":     {},
			"docs/a/[id]/[post]/page.svelte": {},
		}, "ambiguous catch-all"},
		{"mixed static+dynamic under catch-all", fstest.MapFS{
			"docs/[...slug]/page.svelte":  {},
			"docs/guide/[id]/page.svelte": {},
		}, "ambiguous catch-all"},
		{"dynamic deeper than one segment", fstest.MapFS{
			"docs/[...slug]/page.svelte":   {},
			"docs/[id]/[post]/page.svelte": {},
		}, "ambiguous catch-all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Discover(tc.fsys)
			if err == nil {
				t.Fatal("expected catch-all conflict error")
			}
			assertErrContains(t, err, tc.want)
		})
	}
}

// B03: the duplicate-name rule extends to catch-alls — a route cannot reuse a
// named parameter name as its catch-all name.
func TestCatchAllDuplicateNameRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"users/[id]/[...id]/page.svelte": {Data: []byte("x")},
	}
	_, err := Discover(fsys)
	if err == nil || !strings.Contains(err.Error(), "duplicate parameter name") {
		t.Fatalf("expected duplicate parameter name error, got %v", err)
	}
}

// B03: a catch-all counts as one parameter toward Fiber's 30-parameter limit.
func TestCatchAllCountsTowardParamLimit(t *testing.T) {
	parts := make([]string, 30)
	for i := range parts {
		parts[i] = "[" + "p" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + "]"
	}
	// 30 named params + a catch-all = 31 parameters.
	dir := strings.Join(parts, "/") + "/[...rest]"
	_, err := Discover(fstest.MapFS{dir + "/page.svelte": {}})
	if err == nil || !strings.Contains(err.Error(), "at most 30") {
		t.Fatalf("expected parameter limit error, got %v", err)
	}
}

// B03: the catch-all name is deterministic and independent of filesystem
// enumeration order — repeated discovery yields identical descriptors.
func TestCatchAllDiscoveryDeterministic(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	write(t, app, "docs/[...slug]/page.svelte")
	write(t, app, "docs/intro/page.svelte")
	write(t, app, "docs/[id]/page.svelte")
	first, err := Discover(os.DirFS(app))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := Discover(os.DirFS(app))
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if len(got) != len(first) {
			t.Fatalf("iteration %d: route count changed", i)
		}
		for j := range first {
			if first[j].Path != got[j].Path || first[j].CatchAll != got[j].CatchAll || first[j].SourceFile != got[j].SourceFile {
				t.Fatalf("iteration %d differs: %+v vs %+v", i, first[j], got[j])
			}
		}
	}
}
