package render

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/metadata"
	"github.com/gofiber/fiber/v3"
)

func TestPageMetadataOverridesLayoutForDocumentAndNavigation(t *testing.T) {
	route := routing.Route{ID: "page.templ", Path: "/", Mode: routing.ModeSSR}
	pageCount := 0
	page := func(routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			pageCount++
			metadata.Set(ctx, metadata.Page{Title: `A <title> & "quote"`, Canonical: "https://example.com/post?a=1&b=2", OpenGraph: metadata.OpenGraph{Title: "Post", Image: "/images/post.jpg"}, Twitter: metadata.Twitter{Card: "summary_large_image"}})
			_, err := io.WriteString(w, "<h1>Post</h1>")
			return err
		}), nil
	}
	layout := func(_ routing.Route, content templ.Component) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			if err := content.Render(ctx, w); err != nil {
				return err
			}
			metadata.Set(ctx, metadata.Page{Title: "Default", Description: `Safe <text> & "quotes"`})
			return nil
		}), nil
	}
	app := fiber.New()
	composed := func(r routing.Route) (templ.Component, error) {
		component, err := page(r)
		if err != nil {
			return nil, err
		}
		return layout(r, metadata.PageComponent(component))
	}
	app.Get("/", SSRNavigationHandler(route, nil, composed, page, layout))
	for _, navigation := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/", nil)
		if navigation {
			req.Header.Set(httpserver.NavigationHeader, "1")
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if navigation {
			var env httpserver.NavigationResponse
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatal(err)
			}
			if env.Title != `A <title> & "quote"` || env.Metadata == nil || env.Metadata.Description != `Safe <text> & "quotes"` || env.Metadata.OpenGraph.Image != "/images/post.jpg" || env.Metadata.Canonical != "https://example.com/post?a=1&b=2" {
				t.Fatalf("metadata: %+v", env)
			}
		} else {
			for _, want := range []string{`<title>A &lt;title&gt; &amp; &#34;quote&#34;</title>`, `content="Safe &lt;text&gt; &amp; &#34;quotes&#34;"`, `<link rel="canonical" data-meta-managed="canonical" href="https://example.com/post?a=1&amp;b=2">`, `<meta property="og:image" data-meta-managed="og:image" content="/images/post.jpg">`, `<meta name="twitter:card" data-meta-managed="twitter:card" content="summary_large_image">`} {
				if !strings.Contains(string(body), want) {
					t.Fatalf("missing %q in %s", want, body)
				}
			}
		}
	}
	if pageCount != 2 {
		t.Fatalf("rendered page %d times for 2 requests", pageCount)
	}
}
