package main

// F08 pre-task audit: pin the /docs SSR navigation contract.
//
// BarePage() emits ONLY the page component — envelope html never includes
// layout markup. The full document wraps the page in its layouts; the
// envelope's `layouts` array carries the ancestry (root + docs) for F09's
// client-side reconciliation instead. The F07 report wrongly described the
// envelope as including the docs layout; this test pins the actual,
// blueprint-conformant behavior so the wire contract cannot drift.

import (
	"encoding/json"
	"strings"
	"testing"

	"go-meta/internal/httpserver"
)

// TestF08DocsEnvelopeBarePageNoLayouts verifies the /docs navigation
// envelope: bare page html (no docs-layout markup), full two-entry layout
// ancestry in `layouts`.
func TestF08DocsEnvelopeBarePageNoLayouts(t *testing.T) {
	app := newApp(nil)

	status, body, ctype := navGet(t, app, "/docs")
	if status != 200 {
		t.Fatalf("GET /docs with nav header: status %d, want 200", status)
	}
	if !strings.HasPrefix(ctype, "application/json") {
		t.Fatalf("content type = %q, want application/json", ctype)
	}

	var env httpserver.NavigationResponse
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v\nbody: %s", err, body)
	}
	if env.Mode != "ssr" {
		t.Fatalf("mode = %q, want ssr", env.Mode)
	}
	if env.RouteID != "docs/page.templ" {
		t.Errorf("routeId = %q, want docs/page.templ", env.RouteID)
	}

	// The wire contract: html is the BARE page, without layout wrappers.
	if strings.Contains(env.HTML, "data-go-layout-docs") {
		t.Errorf("envelope html must not include docs layout markup; layouts persist client-side and are reconciled via the layouts array (blueprint 5.2 step 9):\n%s", env.HTML)
	}
	if !strings.Contains(env.HTML, "data-go-page-docs") {
		t.Errorf("envelope html must include the docs page content (data-go-page-docs):\n%s", env.HTML)
	}

	// The ancestry is carried in `layouts`, not in `html`.
	if len(env.Layouts) != 2 ||
		env.Layouts[0] != "app/layout.templ" ||
		env.Layouts[1] != "app/docs/layout.templ" {
		t.Errorf("layouts = %v, want [app/layout.templ app/docs/layout.templ]", env.Layouts)
	}

	// And the full document DOES include the layout — proving the two
	// responses genuinely differ in composition.
	docRawStatus, docRaw := navGetRaw(t, app, "/docs", "")
	if docRawStatus != 200 {
		t.Fatalf("GET /docs without header: status %d, want 200", docRawStatus)
	}
	if !strings.Contains(docRaw, "data-go-layout-docs") {
		t.Errorf("full document should render the docs layout wrapper:\n%s", docRaw)
	}
}
