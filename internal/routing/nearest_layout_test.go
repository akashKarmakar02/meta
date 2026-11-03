package routing

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestNearestVisualLayout(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.templ": {}, "page.templ": {},
		"about/page.templ":        {},
		"dashboard/layout.svelte": {}, "dashboard/page.svelte": {},
		"dashboard/settings/layout.svelte": {}, "dashboard/settings/page.svelte": {},
		"reports/layout.templ": {}, "reports/page.svelte": {},
	}
	routes, err := Discover(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"/": "app/layout.templ", "/about": "app/layout.templ",
		"/dashboard":          "app/dashboard/layout.svelte",
		"/dashboard/settings": "app/dashboard/settings/layout.svelte",
		"/reports":            "app/reports/layout.templ",
	}
	if len(routes) != len(want) {
		t.Fatalf("got %d routes, want %d", len(routes), len(want))
	}
	for _, r := range routes {
		if len(r.Layouts) != 1 || r.Layouts[0].ID != want[r.Path] {
			t.Errorf("%s layouts = %+v, want %q", r.Path, r.Layouts, want[r.Path])
		}
	}
}

func TestUnsupportedVisualLayoutCombinations(t *testing.T) {
	tests := []struct {
		name  string
		files fstest.MapFS
		want  []string
	}{
		{"same directory ambiguity", fstest.MapFS{"layout.templ": {}, "layout.svelte": {}, "page.svelte": {}}, []string{"conflicting layouts", "layout.templ", "layout.svelte"}},
		{"templ page in svelte layout", fstest.MapFS{"layout.svelte": {}, "page.templ": {}}, []string{"SSR page", "page.templ", "layout.svelte"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Discover(tc.files)
			if err == nil {
				t.Fatal("expected discovery error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
		})
	}
}
