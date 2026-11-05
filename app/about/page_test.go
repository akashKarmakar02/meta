package about

import (
	"context"
	"strings"
	"testing"

	root "github.com/akashKarmakar02/meta/app"
)

// renderAbout renders the About component through the same
// Component.Render path the Fiber adapter uses (D04 contract).
func renderAbout(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := About().Render(context.Background(), &sb); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	return sb.String()
}

// TestAboutRendersDistinctHeading is D02's acceptance: /about has real
// content distinct from the root page — its own heading and body text,
// visible without JavaScript.
func TestAboutRendersDistinctHeading(t *testing.T) {
	got := renderAbout(t)
	for _, want := range []string{"<h1>About</h1>", "renders templ pages server-side"} {
		if !strings.Contains(got, want) {
			t.Errorf("About() = %q, want substring %q", got, want)
		}
	}
}

// TestAboutDistinctFromRoot pins that /about and / render different
// markup (C05's two-page contract made explicit at content level).
func TestAboutDistinctFromRoot(t *testing.T) {
	var page strings.Builder
	if err := root.Page().Render(context.Background(), &page); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if page.String() == renderAbout(t) {
		t.Error("About and Page render identical HTML; pages must be distinct")
	}
}
