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
		`<!doctype html>`,
		`<html lang="en">`,
		`<head>`,
		`<meta charset="utf-8">`,
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<title>go-meta</title>`,
		`</head>`,
		`<body>`,
		`<main id="p">page body</main>`,
		`</body>`,
		`</html>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("layout output missing %q\noutput:\n%s", want, got)
		}
	}
}

// TestLayoutEscapesNothingByDefault documents that Layout itself adds no text
// interpolation; content slots arrive pre-rendered from child components.
func TestLayoutContentIsComposable(t *testing.T) {
	var b strings.Builder
	if err := Layout(templ.Raw("X")).Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(b.String()), "X</body></html>") {
		t.Errorf("content should be the last element before </body>, got:\n%s", b.String())
	}
}
