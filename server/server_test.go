package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/action"
	"github.com/akashKarmakar02/meta/formcsrf"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/loaderdata"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routing"
	"github.com/akashKarmakar02/meta/server"
)

type input struct {
	Name string `json:"name"`
}
type output struct {
	Name string `json:"name"`
}

func TestRuntimeRoutesLoadersActionsAssetsAndCSRF(t *testing.T) {
	client := t.TempDir()
	for name, data := range map[string]string{
		".vite/manifest.json": `{"frontend/entry.ts":{"file":"assets/entry-a.js","isEntry":true}}`,
		"assets/entry-a.js":   "// client",
	} {
		p := filepath.Join(client, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	csrf, err := formcsrf.New([]byte(strings.Repeat("x", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	var mutations atomic.Int32
	act := func(_ context.Context, _ routecontext.Route, in input) (output, error) {
		mutations.Add(1)
		return output{Name: in.Name}, nil
	}
	resolver := func(r routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			v, _ := loaderdata.Get[string](ctx)
			token, _ := formcsrf.FromContext(ctx)
			_, err := fmt.Fprintf(w, "<h1>%s %s</h1><input name=csrf value=%s>", r.ID, v, token)
			return err
		}), nil
	}
	routes := []routing.Route{
		{ID: "[id]/page.templ", SourceFile: "[id]/page.templ", Path: "/:id", Mode: routing.ModeSSR},
		{ID: "page.templ", SourceFile: "page.templ", Path: "/", Mode: routing.ModeSSR},
		{ID: "about/page.templ", SourceFile: "about/page.templ", Path: "/about", Mode: routing.ModeSSR},
		{ID: "dashboard/page.svelte", SourceFile: "dashboard/page.svelte", Path: "/dashboard", Mode: routing.ModeCSR},
	}
	app, err := server.New(server.Config{
		Routes: routes, SSRPage: resolver, BarePage: resolver,
		VisualLayout: func(_ routing.Route, c templ.Component) (templ.Component, error) { return c, nil },
		Loaders: []loader.Binding{
			loader.Bind("page.templ", func(context.Context, routecontext.Route) (string, error) { return "loaded", nil }),
			loader.Bind("dashboard/page.svelte", func(context.Context, routecontext.Route) (string, error) { return "csr-data", nil }),
		},
		Actions: []action.Binding{
			action.Bind("json", act),
			action.BindForm("form", act, func(v url.Values) (input, error) { return input{Name: v.Get("name")}, nil }, "/about"),
		}, TrustedOrigin: "http://example.com", CSRF: csrf, ClientDir: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(req *http.Request) (*http.Response, string) {
		t.Helper()
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, string(body)
	}
	res, body := request(httptest.NewRequest("GET", "/", nil))
	if res.StatusCode != 200 || !strings.Contains(body, "page.templ loaded") || !strings.Contains(body, "/assets/entry-a.js") {
		t.Fatalf("SSR %d: %s", res.StatusCode, body)
	}
	cookies := res.Cookies()
	if len(cookies) == 0 {
		t.Fatal("CSRF cookie missing")
	}
	var csrfCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == formcsrf.CookieName {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil || !strings.Contains(body, csrfCookie.Value) {
		t.Fatal("issued token missing from form render context")
	}
	res, body = request(httptest.NewRequest("GET", "/about", nil))
	if res.StatusCode != 200 || !strings.Contains(body, "about/page.templ") {
		t.Fatalf("static route precedence %d: %s", res.StatusCode, body)
	}
	res, body = request(httptest.NewRequest("GET", "/dashboard", nil))
	if res.StatusCode != 200 || !strings.Contains(body, "csr-data") {
		t.Fatalf("CSR loader %d: %s", res.StatusCode, body)
	}
	nav := httptest.NewRequest("GET", "/dashboard", nil)
	nav.Header.Set("X-Go-Web-Navigation", "1")
	res, body = request(nav)
	if res.StatusCode != 200 || !strings.Contains(body, `"data":"csr-data"`) || !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("CSR navigation %d: %s", res.StatusCode, body)
	}
	res, body = request(httptest.NewRequest("GET", "/assets/entry-a.js", nil))
	if res.StatusCode != 200 || body != "// client" {
		t.Fatalf("asset %d: %q", res.StatusCode, body)
	}
	req := httptest.NewRequest("POST", "/_meta/actions/json", strings.NewReader(`{"name":"json"}`))
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Content-Type", "application/json")
	res, body = request(req)
	if res.StatusCode != 200 || !strings.Contains(body, `"name":"json"`) {
		t.Fatalf("JSON action %d: %s", res.StatusCode, body)
	}
	req = httptest.NewRequest("POST", "/_meta/actions/json", strings.NewReader(`{"name":"forbidden"}`))
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Content-Type", "application/json")
	res, _ = request(req)
	if res.StatusCode != http.StatusForbidden || mutations.Load() != 1 {
		t.Fatalf("cross-origin action %d, actions %d", res.StatusCode, mutations.Load())
	}
	form := url.Values{"name": {"native"}, formcsrf.FieldName: {csrfCookie.Value}}
	req = httptest.NewRequest("POST", "/_meta/actions/form", strings.NewReader(form.Encode()))
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	res, _ = request(req)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/about" {
		t.Fatalf("form %d: %s", res.StatusCode, res.Header.Get("Location"))
	}
	form.Set(formcsrf.FieldName, "tampered")
	req = httptest.NewRequest("POST", "/_meta/actions/form", strings.NewReader(form.Encode()))
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	res, _ = request(req)
	if res.StatusCode != http.StatusForbidden || mutations.Load() != 2 {
		t.Fatalf("tampered form %d, actions %d", res.StatusCode, mutations.Load())
	}
}

func TestRejectInvalidRuntimeConfiguration(t *testing.T) {
	form := action.BindForm("form", func(context.Context, routecontext.Route, input) (output, error) { return output{}, nil }, func(url.Values) (input, error) { return input{}, nil }, "/")
	for name, cfg := range map[string]server.Config{
		"origin missing":     {Actions: []action.Binding{form}},
		"form csrf missing":  {Actions: []action.Binding{form}, TrustedOrigin: "http://example.com"},
		"invalid origin":     {Actions: []action.Binding{form}, TrustedOrigin: "https://example.com/path"},
		"unknown loader":     {Loaders: []loader.Binding{loader.Bind("missing", func(context.Context, routecontext.Route) (string, error) { return "", nil })}},
		"SSR resolvers":      {Routes: []routing.Route{{ID: "page.templ", SourceFile: "page.templ", Path: "/", Mode: routing.ModeSSR}}},
		"CSR assets":         {Routes: []routing.Route{{ID: "page.svelte", SourceFile: "page.svelte", Path: "/", Mode: routing.ModeCSR}}},
		"missing manifest":   {ClientDir: t.TempDir()},
		"catch-all metadata": {DevOrigin: "http://127.0.0.1:5173", Routes: []routing.Route{{ID: "page.svelte", SourceFile: "page.svelte", Path: "/+", Mode: routing.ModeCSR}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := server.New(cfg); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestHealthCheckPrecedesCatchAll(t *testing.T) {
	resolver := func(r routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, r.ID); return err }), nil
	}
	app, err := server.New(server.Config{
		Routes:  []routing.Route{{ID: "[...path]/page.templ", SourceFile: "[...path]/page.templ", Path: "/+", CatchAll: "path", Mode: routing.ModeSSR}},
		SSRPage: resolver, BarePage: resolver, VisualLayout: func(_ routing.Route, c templ.Component) (templ.Component, error) { return c, nil },
		HealthCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := app.Test(httptest.NewRequest("GET", "/healthz", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("health status %d, body %q, error %v", res.StatusCode, body, err)
	}
}
