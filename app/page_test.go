package app

import (
	"context"
	"strings"
	"testing"
)

// renderToString renders a component through the same
// Component.Render(ctx, io.Writer) path the Fiber adapter will use (D04).
func renderToString(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := Page().Render(context.Background(), &sb); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	return sb.String()
}

// TestPageRendersHTML asserts the templ-generated component produces the
// expected HTML (A07 acceptance: sample templ compiles into Go and
// renders HTML via Go tests).
func TestPageRendersHTML(t *testing.T) {
	got := renderToString(t)
	want := `<main data-go-page-root>Welcome</main>`
	if !strings.Contains(got, want) {
		t.Errorf("render = %q, want substring %q", got, want)
	}
}

// TestPageRenderComponentContext verifies rendering via context.Background()
// with a plain io.Writer, matching the D04 adapter contract.
func TestPageRenderComponentContext(t *testing.T) {
	got := renderToString(t)
	if !strings.Contains(got, "Welcome") {
		t.Errorf("render = %q, want it to contain %q", got, "Welcome")
	}
}
