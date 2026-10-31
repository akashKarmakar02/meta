package routing

import "testing"

func TestRenderModeValid(t *testing.T) {
	for _, m := range []RenderMode{ModeSSR, ModeCSR} {
		if !m.Valid() {
			t.Errorf("Valid(%q) = false, want true", m)
		}
	}
	for _, m := range []RenderMode{"", "static", "SSR", "ssg"} {
		if m.Valid() {
			t.Errorf("Valid(%q) = true, want false", m)
		}
	}
}

func TestNewRoute(t *testing.T) {
	root := Layout{ID: "app/layout.templ", SourceFile: "app/layout.templ", Mode: ModeSSR}
	got, err := NewRoute("/about", "about/page.templ", ModeSSR, []Layout{root})
	if err != nil {
		t.Fatalf("NewRoute: %v", err)
	}
	if got.Path != "/about" || got.SourceFile != "about/page.templ" || got.Mode != ModeSSR {
		t.Errorf("NewRoute fields = %+v", got)
	}
	if len(got.Layouts) != 1 || got.Layouts[0].ID != "app/layout.templ" {
		t.Errorf("Layouts = %+v, want single root layout", got.Layouts)
	}
}

func TestNewRouteLayoutsCopied(t *testing.T) {
	in := []Layout{{ID: "app/layout.templ", SourceFile: "app/layout.templ", Mode: ModeSSR}}
	got, err := NewRoute("/", "page.templ", ModeSSR, in)
	if err != nil {
		t.Fatalf("NewRoute: %v", err)
	}
	in[0] = Layout{}
	if got.Layouts[0].ID != "app/layout.templ" {
		t.Error("NewRoute did not defensively copy layouts slice")
	}
}

func TestNewRouteErrors(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		sourceFile string
		mode       RenderMode
	}{
		{"empty path", "", "page.templ", ModeSSR},
		{"relative path", "about", "about/page.templ", ModeSSR},
		{"empty source", "/about", "", ModeSSR},
		{"invalid mode", "/about", "about/page.templ", RenderMode("static")},
		{"empty mode", "/about", "about/page.templ", ""},
	}
	for _, tc := range cases {
		if _, err := NewRoute(tc.path, tc.sourceFile, tc.mode, nil); err == nil {
			t.Errorf("%s: NewRoute(%q, %q, %q) succeeded, want error", tc.name, tc.path, tc.sourceFile, tc.mode)
		}
	}
}

func TestNewRouteInvalidLayout(t *testing.T) {
	bad := []Layout{
		{SourceFile: "app/layout.templ", Mode: ModeSSR}, // empty ID
		{ID: "app/layout.templ", SourceFile: "app/layout.templ", Mode: RenderMode("ssg")},
	}
	for _, l := range bad {
		if _, err := NewRoute("/", "page.templ", ModeSSR, []Layout{l}); err == nil {
			t.Errorf("NewRoute with layout %+v succeeded, want error", l)
		}
	}
}

func TestNewLayout(t *testing.T) {
	got, err := NewLayout("dashboard/layout.svelte", "dashboard/layout.svelte", ModeCSR)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if got.ID != "dashboard/layout.svelte" || got.Mode != ModeCSR {
		t.Errorf("NewLayout = %+v", got)
	}
	for _, tc := range []struct {
		id, src string
		mode    RenderMode
	}{
		{"", "layout.templ", ModeSSR},
		{"app/layout.templ", "", ModeSSR},
		{"app/layout.templ", "app/layout.templ", "static"},
	} {
		if _, err := NewLayout(tc.id, tc.src, tc.mode); err == nil {
			t.Errorf("NewLayout(%q, %q, %q) succeeded, want error", tc.id, tc.src, tc.mode)
		}
	}
}
