package render

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akashKarmakar02/meta/internal/httpserver"
	"github.com/akashKarmakar02/meta/internal/routing"
	"github.com/gofiber/fiber/v3"
)

func TestDynamicCSRUsesConcreteEscapedURL(t *testing.T) {
	app := fiber.New()
	route := routing.Route{ID: "items/[name]/page.svelte", Path: "/items/:name", Mode: routing.ModeCSR}
	app.Get(route.Path, CSRNavigationHandler(route, nil))
	for _, nav := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/items/A%20B?query=one%20two", nil)
		if nav {
			req.Header.Set(httpserver.NavigationHeader, "1")
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if nav {
			var envelope httpserver.NavigationResponse
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.URL != "/items/A%20B?query=one%20two" || envelope.RouteID != route.ID {
				t.Fatalf("envelope = %+v", envelope)
			}
		} else if !strings.Contains(string(data), `data-go-route="/items/A%20B"`) {
			t.Fatalf("shell = %s", data)
		}
	}
}
