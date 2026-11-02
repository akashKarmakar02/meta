package app

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// TestLayoutOutputsFullDocument is the D01 acceptance test: Layout wraps page
// content in a complete HTML5 document with head metadata and body.
func TestLayoutOutputsFullDocument(t *testing.T) {
	var b strings.Builder
	content := templ.Raw(`<main id="p">page body</main>`)
	if err := Layout(content).Render(context.Background(), &b); err != nil {
		t.Fatalf("render layout: %v", err)
	}
	got := b.String()

	for _, want := range []string{
		`<div data-go-layout="app/layout.templ">`,
		`<nav data-go-site-nav>`,
		`<main id="p">page body</main>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("layout output missing %q\noutput:\n%s", want, got)
		}
	}
}

// TestLayoutContentIsComposable documents that Layout itself adds no text
// interpolation; content slots arrive pre-rendered from child components.
// Since F07 the page slot lives inside the data-go-page-outlet wrapper —
// the server-owned page region soft navigation replaces client-side.
func TestLayoutContentIsComposable(t *testing.T) {
	var b strings.Builder
	if err := Layout(templ.Raw("X")).Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := strings.TrimSpace(b.String())
	wantSuffix := `<nav data-go-site-nav><ul><li><a href="/">Home</a></li><li><a href="/about">About</a></li><li><a href="/docs">Docs</a></li><li><a href="/dashboard">Dashboard</a></li><li><a href="/dashboard/settings">Settings</a></li><li><a href="/products/new">New product</a></li></ul></nav>X</div>`
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("content should render inside the page outlet, last before </body>, want suffix %q, got:\n%s", wantSuffix, got)
	}
}
