package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/action"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/observability"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routing"
	"github.com/gofiber/fiber/v3"
)

func TestObservabilitySecurityIntegration(t *testing.T) {
	var logs bytes.Buffer
	metrics := observability.NewCollector()
	var loads, actions atomic.Int32
	resolver := func(r routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, "protected"); return err }), nil
	}
	app, err := New(Config{
		Observability: &observability.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Metrics: metrics},
		Routes:        []routing.Route{{ID: "private/page.templ", SourceFile: "private/page.templ", Path: "/private", Mode: routing.ModeSSR}},
		SSRPage:       resolver, BarePage: resolver, VisualLayout: func(_ routing.Route, c templ.Component) (templ.Component, error) { return c, nil },
		Middleware: []fiber.Handler{func(c fiber.Ctx) error { c.Set("X-Hook", "yes"); return c.Next() }},
		Policies: []Policy{Protect("/private", func(c fiber.Ctx) error {
			if c.Get("X-Allow") != "yes" {
				return c.SendStatus(403)
			}
			return c.Next()
		})},
		Loaders: []loader.Binding{loader.Bind("private/page.templ", func(ctx context.Context, _ routecontext.Route) (string, error) {
			loads.Add(1)
			if observability.RequestID(ctx) == "" {
				t.Error("missing loader request ID")
			}
			return "ok", nil
		})},
		TrustedOrigin: "https://example.com", Actions: []action.Binding{action.Bind("save", func(ctx context.Context, _ routecontext.Route, _ struct{}) (string, error) {
			actions.Add(1)
			if observability.RequestID(ctx) == "" {
				t.Error("missing action request ID")
			}
			return "ok", nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, nav := range []bool{false, true} {
		req := httptest.NewRequest("GET", "/private?token=private-secret", nil)
		if nav {
			req.Header.Set("X-Go-Web-Navigation", "1")
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 403 || resp.Header.Get("X-Request-ID") == "" || resp.Header.Get("X-Hook") != "yes" {
			t.Fatalf("denial: %d %v", resp.StatusCode, resp.Header)
		}
	}
	req := httptest.NewRequest("GET", "/private", nil)
	req.Header.Set("X-Allow", "yes")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || loads.Load() != 1 {
		t.Fatalf("loader %d %d", resp.StatusCode, loads.Load())
	}
	post := httptest.NewRequest("POST", "/_meta/actions/save", strings.NewReader("{}"))
	post.Header.Set("Origin", "https://bad.example")
	post.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(post)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 || actions.Load() != 0 {
		t.Fatal("origin was not enforced")
	}
	post = httptest.NewRequest("POST", "/_meta/actions/save", strings.NewReader("{}"))
	post.Header.Set("Origin", "https://example.com")
	post.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(post)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || actions.Load() != 1 {
		t.Fatal("action failed")
	}
	if strings.Contains(logs.String(), "private-secret") {
		t.Fatal("query leaked into logs")
	}
	if len(metrics.Snapshot()) == 0 {
		t.Fatal("metrics missing")
	}
}
