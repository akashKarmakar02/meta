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
	"github.com/gofiber/fiber/v3"
)

func TestSSRNavigationRendersPageOnce(t *testing.T) {
	app := fiber.New()
	route := routing.Route{ID: "page.templ", Path: "/", Mode: routing.ModeSSR}
	renders := 0
	page := func(routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
			renders++
			_, err := io.WriteString(w, "<main><h1>Page</h1></main>")
			return err
		}), nil
	}
	visual := func(_ routing.Route, content templ.Component) (templ.Component, error) {
		return templ.Join(templ.Raw("<section>"), content, templ.Raw("</section>")), nil
	}
	app.Get("/", SSRNavigationHandler(route, nil, page, page, visual))
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set(httpserver.NavigationHeader, "1")
	resp, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	var env httpserver.NavigationResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if renders != 1 || env.Title != "Page" || env.HTML != "<section><main><h1>Page</h1></main></section>" {
		t.Fatalf("renders = %d, response = %+v", renders, env)
	}
}

func TestCSRDocumentWithoutVisualLayout(t *testing.T) {
	app := fiber.New()
	app.Get("/", CSRNavigationHandler(routing.Route{ID: "page.svelte", Path: "/", Mode: routing.ModeCSR}, nil))
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<!DOCTYPE html>", "<html", "<head>", "<body>", "data-go-page-outlet", "data-go-page-root"} {
		if strings.Count(string(data), want) != 1 {
			t.Errorf("document %q must contain one %q", data, want)
		}
	}
}

func TestCSRDocumentWithTemplLayout(t *testing.T) {
	app := fiber.New()
	route := routing.Route{ID: "page.svelte", Path: "/", Mode: routing.ModeCSR, Layouts: []routing.Layout{{ID: "app/layout.templ", Mode: routing.ModeSSR}}}
	resolve := func(_ routing.Route, child templ.Component) (templ.Component, error) {
		return templ.Join(templ.Raw(`<section data-go-layout="app/layout.templ">`), child, templ.Raw(`</section>`)), nil
	}
	app.Get("/", CSRNavigationHandler(route, nil, resolve))
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `data-go-layout="app/layout.templ"`) || !strings.Contains(string(data), "data-go-page-root") {
		t.Fatalf("missing layout or client mount: %s", data)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set(httpserver.NavigationHeader, "1")
	resp, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	var env httpserver.NavigationResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Mode != "csr" || !strings.Contains(env.HTML, `data-go-layout="app/layout.templ"`) || !strings.Contains(env.HTML, "data-go-page-root") {
		t.Fatalf("CSR envelope missing templ visual layout and mount: %+v", env)
	}
}
