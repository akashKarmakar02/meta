package render

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routeparams"
	"github.com/gofiber/fiber/v3"
)

func TestStaticSSRReceivesRouteContext(t *testing.T) {
	app := fiber.New()
	route := routing.Route{ID: "page.templ", SourceFile: "page.templ", Path: "/", Mode: routing.ModeSSR}
	page := func(routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			matched, ok := routecontext.FromContext(ctx)
			if !ok || len(routeparams.FromContext(ctx).All()) != 0 {
				return fmt.Errorf("missing static route context")
			}
			_, err := fmt.Fprintf(w, "<main>%s|%s|%s</main>", matched.RouteID, matched.Pattern, matched.Query.Get("tab"))
			return err
		}), nil
	}
	visual := func(_ routing.Route, child templ.Component) (templ.Component, error) { return child, nil }
	app.Get("/", httpserver.WithRouteParams(route, SSRNavigationHandler(route, nil, page, page, visual)))
	for _, nav := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/?tab=overview", nil)
		if nav {
			req.Header.Set(httpserver.NavigationHeader, "1")
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || !strings.Contains(string(body), "page.templ|/|overview") {
			t.Fatalf("status=%d body=%s", res.StatusCode, body)
		}
	}
}
