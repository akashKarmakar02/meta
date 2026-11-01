package httpserver

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// fixtureEnvelope loads a checked-in fixture and verifies it parses into
// the Go envelope with the exact field values pinned.
func fixtureEnvelope(t *testing.T, name string) NavigationResponse {
	t.Helper()
	b, err := os.ReadFile("../../tests/fixtures/" + name)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := UnmarshalNavigation(b)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return resp
}

// TestNavigationSSRFixture pins the SSR wire shape (F01): a full
// server-owned HTML string, no client entry, one layout.
func TestNavigationSSRFixture(t *testing.T) {
	resp := fixtureEnvelope(t, "navigation-ssr.json")
	if resp.Version != 1 {
		t.Errorf("version = %d, want 1", resp.Version)
	}
	if resp.URL != "/about" || resp.RouteID != "about/page.templ" {
		t.Errorf("url/routeId = %q/%q", resp.URL, resp.RouteID)
	}
	if resp.Mode != "ssr" {
		t.Errorf("mode = %q, want ssr", resp.Mode)
	}
	if resp.Title != "About" {
		t.Errorf("title = %q", resp.Title)
	}
	if len(resp.Layouts) != 1 || resp.Layouts[0] != "app/layout.templ" {
		t.Errorf("layouts = %v", resp.Layouts)
	}
	if !strings.Contains(resp.HTML, "<h1>About</h1>") {
		t.Errorf("html missing content: %q", resp.HTML)
	}
	if resp.ClientEntry != nil {
		t.Errorf("clientEntry = %v, want nil for SSR", resp.ClientEntry)
	}
}

// TestNavigationCSRFixture pins the CSR wire shape (F01): empty html,
// clientEntry naming the page module, layout chain included.
func TestNavigationCSRFixture(t *testing.T) {
	resp := fixtureEnvelope(t, "navigation-csr.json")
	if resp.Version != 1 {
		t.Errorf("version = %d", resp.Version)
	}
	if resp.URL != "/dashboard/settings" || resp.RouteID != "dashboard/settings/page.svelte" {
		t.Errorf("url/routeId = %q/%q", resp.URL, resp.RouteID)
	}
	if resp.Mode != "csr" {
		t.Errorf("mode = %q, want csr", resp.Mode)
	}
	if resp.HTML != "" {
		t.Errorf("html = %q, want empty for CSR", resp.HTML)
	}
	entry := "dashboard/settings/page.svelte"
	if resp.ClientEntry == nil || *resp.ClientEntry != entry {
		t.Errorf("clientEntry = %v, want %q", resp.ClientEntry, entry)
	}
	wantLayouts := []string{"app/layout.templ"}
	if len(resp.Layouts) != 1 || resp.Layouts[0] != wantLayouts[0] {
		t.Errorf("layouts = %v, want %v", resp.Layouts, wantLayouts)
	}
}

// TestNavigationRoundTrip: MarshalNavigation produces the exact wire
// shape the fixtures pin — round-tripping an envelope must reproduce the
// fixture bytes modulo key order stability of encoding/json.
func TestNavigationRoundTrip(t *testing.T) {
	for _, name := range []string{"navigation-ssr.json", "navigation-csr.json"} {
		fixtureBytes, err := os.ReadFile("../../tests/fixtures/" + name)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := UnmarshalNavigation(fixtureBytes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := MarshalNavigation(resp)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Compare structurally (field order is fixed by struct definition).
		var fromFixture, fromRoundTrip interface{}
		if err := json.Unmarshal(fixtureBytes, &fromFixture); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got, &fromRoundTrip); err != nil {
			t.Fatal(err)
		}
		if e, a := fmtJSON(t, fromFixture), fmtJSON(t, fromRoundTrip); e != a {
			t.Fatalf("%s round-trip drift:\nwant %s\ngot  %s", name, e, a)
		}
	}
}

// fmtJSON re-serializes with sorted keys for stable comparison.
func fmtJSON(t *testing.T, v interface{}) string {
	t.Helper()
	var m map[string]interface{}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	out, err := json.MarshalIndent(m, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestNavigationVersionConstant: the protocol version the Go side speaks.
func TestNavigationVersionConstant(t *testing.T) {
	if NavigationVersion != 1 {
		t.Errorf("NavigationVersion = %d, want 1", NavigationVersion)
	}
}
