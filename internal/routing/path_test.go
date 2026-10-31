package routing

import (
	"testing"
	"testing/fstest"
)

// TestNormalizeDirToPath is the B10 path normalization test table.
func TestNormalizeDirToPath(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{"", "/"},            // app root
		{".", "/"},           // explicit current dir
		{"./", "/"},          // current dir with slash
		{"about/", "/about"}, // single segment
		{"a/b/", "/a/b"},     // nested segments
		{"a/b/c/", "/a/b/c"}, // deep nesting
	}
	for _, tc := range cases {
		if got := normalizeDirToPath(tc.dir); got != tc.want {
			t.Errorf("normalizeDirToPath(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// TestDiscoverPathTable is the B10 acceptance fixture at discovery level:
// each directory's page resolves to exactly its normalized path, and "/"
// stays distinct from every nested path.
func TestDiscoverPathTable(t *testing.T) {
	fsys := fstest.MapFS{
		"page.templ":                     &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":               &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/page.svelte":          &fstest.MapFile{Data: []byte("<main />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := map[string]string{ // path -> source file
		"/":                   "page.templ",
		"/about":              "about/page.templ",
		"/dashboard":          "dashboard/page.svelte",
		"/dashboard/settings": "dashboard/settings/page.svelte",
	}
	if len(routes) != len(want) {
		t.Fatalf("routes = %d, want %d; got %+v", len(routes), len(want), routes)
	}
	seen := map[string]string{}
	for _, r := range routes {
		if src, dup := seen[r.Path]; dup {
			t.Errorf("path %q discovered twice (from %q and %q)", r.Path, src, r.SourceFile)
			continue
		}
		seen[r.Path] = r.SourceFile
		if wantSrc, ok := want[r.Path]; !ok {
			t.Errorf("unexpected path %q from %q", r.Path, r.SourceFile)
		} else if r.SourceFile != wantSrc {
			t.Errorf("path %q sourced from %q, want %q", r.Path, r.SourceFile, wantSrc)
		}
	}
	for p := range want {
		if _, ok := seen[p]; !ok {
			t.Errorf("expected path %q not discovered", p)
		}
	}

	// "/" and "/about" must be distinct routes.
	if _, hasRoot := seen["/"]; !hasRoot {
		t.Error(`route "/" missing`)
	}
	if _, hasAbout := seen["/about"]; !hasAbout {
		t.Error(`route "/about" missing`)
	}
}
