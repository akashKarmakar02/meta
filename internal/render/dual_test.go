package render

import (
	"context"
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

func TestPairCapturesPageOnceInsideLayoutContext(t *testing.T) {
	type layoutKey struct{}
	route := routing.Route{ID: "page.templ", Path: "/", Mode: routing.ModeSSR}
	pages, layouts := 0, 0
	bare := func(routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			pages++
			if ctx.Value(layoutKey{}) != "provided" {
				t.Error("lost layout context")
			}
			metadata.Set(ctx, metadata.Page{Title: "Page", Canonical: "/canonical"})
			_, err := io.WriteString(w, "<h1>Page</h1>")
			return err
		}), nil
	}
	visual := func(_ routing.Route, child templ.Component) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			layouts++
			ctx = context.WithValue(ctx, layoutKey{}, "provided")
			if err := child.Render(ctx, w); err != nil {
				return err
			}
			// Custom repeated outlets replay bytes, not side-effecting page code.
			if err := child.Render(ctx, w); err != nil {
				return err
			}
			metadata.Set(ctx, metadata.Page{Title: "Default", Description: "Inherited"})
			return nil
		}), nil
	}
	generate := SSRPairGenerator(route, &httpserver.Assets{EntryScriptURL: "/assets/app-hash.js", EntryCSSURLs: []string{"/assets/app-hash.css"}}, bare, visual)
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		pair, err := generate(c)
		if err != nil {
			return err
		}
		env, err := httpserver.UnmarshalNavigation(pair.Navigation.Body)
		if err != nil {
			return err
		}
		if env.Title != "Page" || env.Metadata.Description != "Inherited" || env.Metadata.Canonical != "/canonical" {
			t.Fatalf("metadata %+v", env)
		}
		if strings.Count(env.HTML, "<h1>Page</h1>") != 2 {
			t.Fatal("layout output changed")
		}
		for _, want := range []string{"<title>Page</title>", "/assets/app-hash.js", "/assets/app-hash.css", `href="/canonical"`} {
			if !strings.Contains(string(pair.HTML.Body), want) {
				t.Errorf("missing %q", want)
			}
		}
		return c.Send(pair.HTML.Body)
	})
	res, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || pages != 1 || layouts != 1 {
		t.Fatalf("status/pages/layouts %d/%d/%d", res.StatusCode, pages, layouts)
	}
}
