package metastate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSEOFieldInheritanceAndSafeURLs(t *testing.T) {
	ctx := New(context.Background())
	Set(ctx, Page{Canonical: "https://example.com/default", OpenGraph: OpenGraph{Title: "Layout", Image: "/images/default.jpg"}, Twitter: Twitter{Card: "summary", Site: "@site"}})
	Set(PageContext(ctx), Page{Canonical: "javascript:alert(1)", OpenGraph: OpenGraph{Title: "Page", Image: "//evil.example/image.jpg"}, Twitter: Twitter{Card: "summary_large_image"}})
	got := Get(ctx)
	if got.Canonical != "https://example.com/default" || got.OpenGraph.Title != "Page" || got.OpenGraph.Image != "/images/default.jpg" || got.Twitter.Card != "summary_large_image" || got.Twitter.Site != "@site" {
		t.Fatalf("merged metadata: %+v", got)
	}
	for _, value := range []string{"javascript:alert(1)", "//evil.example/a", "data:text/html,bad", "https://user:pass@example.com", "/\\evil", "https://example.com/\n"} {
		if SafeURL(value) != "" {
			t.Errorf("accepted unsafe URL %q", value)
		}
	}
	for _, value := range []string{"https://example.com/a?q=one", "http://localhost:3000/a", "/images/hero.jpg"} {
		if SafeURL(value) != value {
			t.Errorf("rejected URL %q", value)
		}
	}
}

func TestEmptySocialFieldsOmittedFromJSON(t *testing.T) {
	data, err := json.Marshal(Page{Title: "Title"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "openGraph") || strings.Contains(string(data), "twitter") {
		t.Fatalf("unexpected empty fields: %s", data)
	}
}
