package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/akashKarmakar02/meta/imageopt"
	"github.com/gofiber/fiber/v3"
)

func TestImageEndpoint(t *testing.T) {
	app := fiber.New()
	MountImage(app)
	var pages atomic.Int32
	app.Get("/*", func(c fiber.Ctx) error { pages.Add(1); return c.SendString("page") })
	app.Post("/_meta/actions/*", func(c fiber.Ctx) error { return c.SendString("action") })
	for _, tc := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"GET", "/_meta/image?src=%2Fimages%2Fa.jpg&w=50", 501, "not implemented"},
		{"HEAD", "/_meta/image?src=%2Fimages%2Fa.jpg&w=50", 501, ""},
		{"GET", "/_meta/image?src=%2Fimages%2Fa.jpg&w=0", 400, "invalid image request"},
		{"GET", "/_meta/image?src=%2Fimages%2F..%2Fsecret&w=50", 400, "invalid image request"},
		{"GET", "/_meta/image?src=%2Fimages%2Fa&w=1&f=bogus", 400, "invalid image request"},
		{"GET", "/_meta/image?w=1", 400, "invalid image request"},
		{"POST", "/_meta/image", 405, "method not allowed"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("X-Go-Web-Navigation", "1")
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != tc.status || !strings.Contains(string(body), tc.body) {
				t.Fatalf("%d %s", res.StatusCode, body)
			}
			if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") || res.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("headers %v", res.Header)
			}
			if strings.Contains(string(body), "secret") || strings.Contains(string(body), "routeId") {
				t.Fatal("unexpected disclosure or envelope")
			}
			if tc.method == "POST" && res.Header.Get("Allow") != "GET, HEAD" {
				t.Fatal("missing allow")
			}
		})
	}
	if pages.Load() != 0 {
		t.Fatal("page catch-all ran")
	}
	res, err := app.Test(httptest.NewRequest(http.MethodPost, "/_meta/actions/unknown", nil))
	if err != nil || res.StatusCode != 200 {
		t.Fatal("action route affected", err)
	}
	_ = res.Body.Close()
}

func TestImageIndependentServerLimits(t *testing.T) {
	app := fiber.New()
	limits := imageopt.DefaultLimits()
	limits.MaxWidth = 100
	if err := MountImageWithLimits(app, limits); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/_meta/image?src=%2Fimages%2Fa&w=101", nil)
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if err := MountImageWithLimits(fiber.New(), imageopt.Limits{}); err == nil {
		t.Fatal("accepted invalid limits")
	}
}
