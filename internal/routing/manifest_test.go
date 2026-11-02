package routing

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func manifestTree() fstest.MapFS {
	return fstest.MapFS{
		"layout.templ":                   &fstest.MapFile{Data: []byte("templ Root() {}")},
		"page.templ":                     &fstest.MapFile{Data: []byte("templ Page() {}")},
		"about/page.templ":               &fstest.MapFile{Data: []byte("templ About() {}")},
		"dashboard/layout.svelte":        &fstest.MapFile{Data: []byte("<div />")},
		"dashboard/page.svelte":          &fstest.MapFile{Data: []byte("<main />")},
		"dashboard/settings/page.svelte": &fstest.MapFile{Data: []byte("<main />")},
	}
}

// TestManifestJSONSerialization is the C01 acceptance fixture: routes
// serialize to JSON with complete route and layout metadata, stable IDs, and
// no absolute paths.
func TestManifestJSONSerialization(t *testing.T) {
	routes, err := Discover(manifestTree())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	m := NewManifest(routes)

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)

	// Version stamp.
	if !strings.Contains(s, `"version":1`) {
		t.Errorf("manifest missing version 1: %s", s)
	}

	// Complete metadata for every route, in order.
	for _, want := range []string{
		`"id":"page.templ"`,
		`"path":"/"`,
		`"sourceFile":"page.templ"`,
		`"mode":"ssr"`,
		`"id":"about/page.templ"`,
		`"path":"/about"`,
		`"id":"dashboard/page.svelte"`,
		`"path":"/dashboard"`,
		`"mode":"csr"`,
		`"id":"dashboard/settings/page.svelte"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}

	// Layout metadata: IDs, source files, modes, chain order outermost-first.
	for _, want := range []string{
		`"id":"app/layout.templ"`,
		`"id":"app/dashboard/layout.svelte"`,
		`"sourceFile":"dashboard/layout.svelte"`,
		`"mode":"csr"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing layout field %q:\n%s", want, s)
		}
	}
	dashIdx := strings.Index(s, `"path":"/dashboard"`)
	settingsIdx := strings.Index(s, `"path":"/dashboard/settings"`)
	rootLayoutIdx := strings.Index(s, `"id":"app/layout.templ"`)
	dashLayoutIdx := strings.Index(s, `"id":"app/dashboard/layout.svelte"`)
	if rootLayoutIdx == -1 || dashLayoutIdx == -1 || dashIdx == -1 || settingsIdx == -1 {
		t.Fatalf("route or layout fields not found in manifest")
	}
	if !(rootLayoutIdx < dashLayoutIdx) {
		t.Errorf("layout order not outermost-first in JSON: layout.templ@%d, dashboard/layout.svelte@%d",
			rootLayoutIdx, dashLayoutIdx)
	}

	// No absolute paths leak into the manifest.
	if strings.Contains(s, `"/home/`) || strings.Contains(s, `"/Users/`) ||
		strings.Contains(s, `C:\\`) || strings.Contains(s, `\\`) {
		t.Errorf("manifest leaks absolute path: %s", s)
	}
}

// TestManifestRoundTrip verifies valid deserialization: the manifest schema
// must round-trip a discovered route set exactly.
func TestManifestRoundTrip(t *testing.T) {
	routes, err := Discover(manifestTree())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	m := NewManifest(routes)

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Manifest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(m, back) {
		t.Errorf("round-trip mismatch:\n%+v\n%+v", m, back)
	}
	if back.Version != ManifestVersion {
		t.Errorf("version = %d, want %d", back.Version, ManifestVersion)
	}
}

// TestManifestDeterministicOutput: identical inputs serialize to identical
// bytes, every time.
func TestManifestDeterministicOutput(t *testing.T) {
	first, err := Discover(manifestTree())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want, err := json.Marshal(NewManifest(first))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for i := 0; i < 50; i++ {
		routes, err := Discover(manifestTree())
		if err != nil {
			t.Fatalf("Discover %d: %v", i, err)
		}
		got, err := json.Marshal(NewManifest(routes))
		if err != nil {
			t.Fatalf("Marshal %d: %v", i, err)
		}
		if string(got) != string(want) {
			t.Fatalf("iteration %d differs:\ngot:  %s\nwant: %s", i, got, want)
		}
	}
}

// TestManifestEmptyRoutes serializes as an empty list, not null.
func TestManifestEmptyRoutes(t *testing.T) {
	data, err := json.Marshal(NewManifest(nil))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"routes":[]`) {
		t.Errorf("empty manifest = %s, want routes:[]", data)
	}
}

// TestRouteStableIDs locks ID stability: IDs equal source paths relative to
// the app root and match the blueprint's wire-format examples.
func TestRouteStableIDs(t *testing.T) {
	routes, err := Discover(manifestTree())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	byPath := map[string]Route{}
	for _, r := range routes {
		byPath[r.Path] = r
	}
	for path, wantID := range map[string]string{
		"/":                   "page.templ",
		"/about":              "about/page.templ",
		"/dashboard":          "dashboard/page.svelte",
		"/dashboard/settings": "dashboard/settings/page.svelte",
	} {
		r, ok := byPath[path]
		if !ok {
			t.Fatalf("route %s missing", path)
		}
		if r.ID != wantID {
			t.Errorf("route %s ID = %q, want %q", path, r.ID, wantID)
		}
	}
	if byPath["/dashboard"].Layouts[0].ID != "app/dashboard/layout.svelte" {
		t.Errorf("nested layout ID = %q, want app/dashboard/layout.svelte",
			byPath["/dashboard"].Layouts[0].ID)
	}
}
