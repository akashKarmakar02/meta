// CORS integration tests: Fiber's cors middleware applied through
// server.Config.Middleware, the supported application API. Meta ships no CORS
// engine of its own; these tests pin the integration contract:
//
//   - Opt-in only; no middleware slice means no approval headers, ever.
//   - Preflight is answered by the middleware even when the file-based route
//     declares no OPTIONS handler.
//   - Approval and authorization stay separate: actions keep their
//     trusted-origin gate, forms keep CSRF, route policies still deny actual
//     requests after a successful preflight.
package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"

	"github.com/akashKarmakar02/meta/action"
	"github.com/akashKarmakar02/meta/formcsrf"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routing"
	"github.com/akashKarmakar02/meta/server"
)

const (
	corsAllowedOrigin  = "https://app.example.com"
	corsBlockedOrigin  = "https://evil.example.net"
	corsTrustedOrigin  = "https://app.example.com"
	corsForeignTrusted = "https://meta.example.org"
)

// corsMiddleware is the exact configuration shape documented in
// docs/guides/cors.md: explicit origins, explicit methods and headers,
// credentialed, non-zero MaxAge.
func corsMiddleware() fiber.Handler {
	return cors.New(cors.Config{
		AllowOrigins:     []string{corsAllowedOrigin},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           600,
	})
}

type corsFixture struct {
	app       *fiber.App
	mutations *atomic.Int32
}

// newCORSServer builds a real application through server.New with two API
// routes (one static, one dynamic), one SSR page, a JSON action, and a form
// action behind a CSRF manager. trustedOrigin selects whether the CORS-allowed
// origin is also the action-trusted origin (it must be for same-origin action
// success tests) or a foreign one (cross-origin rejection tests).
func newCORSServer(t *testing.T, trustedOrigin string, middleware ...fiber.Handler) corsFixture {
	t.Helper()
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
			_, err := fmt.Fprintf(w, "<h1>%s</h1>", r.ID)
			return err
		}), nil
	}
	apiHandler := func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) }
	app, err := server.New(server.Config{
		Routes: []routing.Route{{ID: "page.templ", SourceFile: "page.templ", Path: "/", Mode: routing.ModeSSR}},
		APIRoutes: []server.APIRoute{
			{Path: "/api/posts", SourceFile: "api/posts/route.go", Handlers: map[string]fiber.Handler{
				"GET": apiHandler, "POST": apiHandler,
			}},
			{Path: "/api/posts/:id", SourceFile: "api/posts/[id]/route.go", Handlers: map[string]fiber.Handler{
				"GET": apiHandler, "PATCH": apiHandler,
			}},
		},
		SSRPage: resolver, BarePage: resolver,
		VisualLayout: func(_ routing.Route, c templ.Component) (templ.Component, error) { return c, nil },
		Middleware:   append([]fiber.Handler(nil), middleware...),
		Actions: []action.Binding{
			action.Bind("test.act", act),
			action.BindForm("test.form", act, func(v url.Values) (input, error) { return input{Name: v.Get("name")}, nil }, "/"),
		},
		TrustedOrigin:      trustedOrigin,
		CSRF:               csrf,
		MaxActionBodyBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	return corsFixture{app: app, mutations: &mutations}
}

func (f corsFixture) do(t *testing.T, method, path, origin string, headers map[string]string, body string) (*http.Response, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := f.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(raw)
}

// TestCORSNotConfigured: without any middleware there is no automatic
// approval. Meta never adds CORS headers on its own.
func TestCORSNotConfigured(t *testing.T) {
	f := newCORSServer(t, corsTrustedOrigin)
	for _, tc := range []struct {
		name, method, path string
		headers            map[string]string
	}{
		{name: "same-origin GET", method: "GET", path: "/api/posts", headers: map[string]string{"Origin": corsTrustedOrigin}},
		{name: "cross-origin GET", method: "GET", path: "/api/posts", headers: map[string]string{"Origin": corsBlockedOrigin}},
		{name: "preflight", method: "OPTIONS", path: "/api/posts", headers: map[string]string{
			"Origin": corsTrustedOrigin, "Access-Control-Request-Method": "POST"}},
	} {
		res, _ := f.do(t, tc.method, tc.path, "", tc.headers, "")
		if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s: unexpected approval %q", tc.name, got)
		}
		if res.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s: unexpected credentials approval", tc.name)
		}
	}
}

// TestCORSSimpleRequests covers actual (non-preflight) API requests: approval
// for the configured origin only, exact-origin echo for credentials, nothing
// for missing origins, and correct behavior on dynamic routes.
func TestCORSSimpleRequests(t *testing.T) {
	f := newCORSServer(t, corsTrustedOrigin, corsMiddleware())

	res, body := f.do(t, "GET", "/api/posts", corsAllowedOrigin, nil, "")
	if res.StatusCode != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("allowed GET: %d %s", res.StatusCode, body)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != corsAllowedOrigin {
		t.Fatalf("allowed GET ACAO = %q", got)
	}
	if res.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("allowed GET missing credentials approval")
	}
	if !strings.Contains(res.Header.Get("Vary"), "Origin") {
		t.Fatalf("allowed GET Vary = %q, want Origin (cache-safe for credential approval)", res.Header.Get("Vary"))
	}

	res, body = f.do(t, "GET", "/api/posts/42", corsAllowedOrigin, nil, "")
	if res.StatusCode != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("dynamic route allowed GET: %d %s", res.StatusCode, body)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != corsAllowedOrigin {
		t.Fatalf("dynamic route ACAO = %q", got)
	}

	res, _ = f.do(t, "GET", "/api/posts", corsBlockedOrigin, nil, "")
	if res.StatusCode != 200 {
		t.Fatalf("disallowed GET status = %d, want 200 (CORS is browser-enforced, not a server block)", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("disallowed GET carries approval headers")
	}
	if !strings.Contains(res.Header.Get("Vary"), "Origin") {
		t.Fatalf("disallowed GET Vary = %q", res.Header.Get("Vary"))
	}

	res, _ = f.do(t, "GET", "/api/posts", "", nil, "")
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("missing-origin GET carries approval")
	}

	res, _ = f.do(t, "POST", "/api/posts", corsAllowedOrigin, map[string]string{"Content-Type": "application/json"}, `{"t":1}`)
	if res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != corsAllowedOrigin {
		t.Fatalf("allowed POST: %d ACAO=%q", res.StatusCode, res.Header.Get("Access-Control-Allow-Origin"))
	}
}

// TestCORSPreflightRequests covers OPTIONS handling. The middleware answers a
// valid preflight itself — 204 with the full header set — including on routes
// that declare no OPTIONS handler, while an OPTIONS request without the
// Access-Control-Request-Method marker stays outside CORS scope and continues
// to the route's own 405.
func TestCORSPreflightRequests(t *testing.T) {
	f := newCORSServer(t, corsTrustedOrigin, corsMiddleware())
	preflight := func(path, origin, requestMethod, requestHeaders string) *http.Response {
		headers := map[string]string{"Access-Control-Request-Method": requestMethod}
		if requestHeaders != "" {
			headers["Access-Control-Request-Headers"] = requestHeaders
		}
		res, _ := f.do(t, "OPTIONS", path, origin, headers, "")
		return res
	}

	for _, tc := range []struct {
		name, path, method string
	}{
		{"static POST", "/api/posts", "POST"},
		{"static PATCH", "/api/posts", "PATCH"},
		{"dynamic route", "/api/posts/42", "PATCH"},
		{"action endpoint", "/_meta/actions/test.act", "POST"},
	} {
		res := preflight(tc.path, corsAllowedOrigin, tc.method, "Authorization, Content-Type")
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("%s preflight status = %d", tc.name, res.StatusCode)
		}
		if got := res.Header.Get("Access-Control-Allow-Origin"); got != corsAllowedOrigin {
			t.Fatalf("%s ACAO = %q", tc.name, got)
		}
		if res.Header.Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatalf("%s missing credentials approval", tc.name)
		}
		if got := res.Header.Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, PATCH, DELETE, OPTIONS" {
			t.Fatalf("%s ACAM = %q", tc.name, got)
		}
		if got := res.Header.Get("Access-Control-Allow-Headers"); got != "Origin, Content-Type, Authorization" {
			t.Fatalf("%s ACAH = %q", tc.name, got)
		}
		if got := res.Header.Get("Access-Control-Max-Age"); got != "600" {
			t.Fatalf("%s Max-Age = %q", tc.name, got)
		}
	}
	// Preflight responses must Vary on every request header that changes them;
	// a shared cache could otherwise serve one origin's approval to another.
	vary := strings.Split(preflight("/api/posts", corsAllowedOrigin, "POST", "").Header.Get("Vary"), ", ")
	for _, want := range []string{"Origin", "Access-Control-Request-Method"} {
		found := false
		for _, got := range vary {
			if strings.TrimSpace(got) == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("preflight Vary = %v, want %q among fields", vary, want)
		}
	}

	// A disallowed origin gets no approving headers. The status is still 204
	// (Fiber terminates preflights) — browsers, not the server, reject the
	// response; endpoint authorization must remain independent.
	res := preflight("/api/posts", corsBlockedOrigin, "POST", "Content-Type")
	if res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("disallowed preflight carries approval headers")
	}

	// An unsupported requested method is never listed as allowed. The server
	// still answers 204 (browsers reject the preflight when the requested
	// method is absent from Access-Control-Allow-Methods — that check is the
	// browser's job, not the server's).
	res = preflight("/api/posts", corsAllowedOrigin, "TRACE", "Content-Type")
	if got := res.Header.Get("Access-Control-Allow-Methods"); strings.Contains(got, "TRACE") {
		t.Fatalf("unconfigured method listed in ACAM = %q", got)
	}

	// OPTIONS without the preflight marker stays outside CORS scope and
	// continues into the route, which answers 405 from its own handler chain.
	res, _ = f.do(t, "OPTIONS", "/api/posts", corsAllowedOrigin, nil, "")
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("non-preflight OPTIONS status = %d, want 405 (route-owned)", res.StatusCode)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("non-preflight OPTIONS carries approval")
	}
}

// TestCORSWildcardWithCredentialsPanics pins the fail-closed property at
// construction: Fiber panics instead of silently serving wildcard approvals
// to credentialed requests. A panic surfaces at startup, never as a permissive
// fallback at request time.
func TestCORSWildcardWithCredentialsPanics(t *testing.T) {
	defer func() {
		r := recover()
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "wildcard") {
			t.Fatalf("recover() = %v, want wildcard/credentials panic", r)
		}
	}()
	cors.New(cors.Config{AllowOrigins: []string{"*"}, AllowCredentials: true})
	t.Fatal("construction should have panicked")
}

// TestCORSPolicyStillDeniesAfterPreflight proves a successful preflight does
// not authorize the actual request: the policy protecting the API still
// rejects the follow-up GET even after the middleware answered its preflight.
func TestCORSPolicyStillDeniesAfterPreflight(t *testing.T) {
	csrf, err := formcsrf.New([]byte(strings.Repeat("x", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	apiHandler := func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) }
	app, err := server.New(server.Config{
		Routes: []routing.Route{{ID: "page.templ", SourceFile: "page.templ", Path: "/", Mode: routing.ModeSSR}},
		APIRoutes: []server.APIRoute{{Path: "/api/posts", SourceFile: "api/posts/route.go",
			Handlers: map[string]fiber.Handler{"GET": apiHandler}}},
		SSRPage:       func(r routing.Route) (templ.Component, error) { return nil, nil },
		BarePage:      func(r routing.Route) (templ.Component, error) { return nil, nil },
		VisualLayout:  func(_ routing.Route, c templ.Component) (templ.Component, error) { return c, nil },
		Middleware:    []fiber.Handler{corsMiddleware()},
		Policies:      []server.Policy{server.Protect("/api/posts/*", func(c fiber.Ctx) error { return c.SendStatus(http.StatusForbidden) })},
		TrustedOrigin: corsTrustedOrigin, CSRF: csrf,
	})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method string, headers map[string]string) *http.Response {
		req := httptest.NewRequest(method, "/api/posts", nil)
		req.Header.Set("Origin", corsAllowedOrigin)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	// Preflight succeeds: the middleware answers it before any policy runs.
	res := do("OPTIONS", map[string]string{"Access-Control-Request-Method": "GET"})
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != corsAllowedOrigin {
		t.Fatalf("preflight: %d", res.StatusCode)
	}
	// The actual request remains denied by the policy regardless.
	res = do("GET", nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("actual GET after approved preflight: %d, want 403", res.StatusCode)
	}
}

// TestCORSDoesNotAuthorizeActions separates CORS approval from action
// authorization: with the action trusted on a foreign origin, the CORS-allowed
// origin gets preflight approval yet its POST is rejected by the trusted-origin
// gate, and no action executes.
func TestCORSDoesNotAuthorizeActions(t *testing.T) {
	f := newCORSServer(t, corsForeignTrusted, corsMiddleware())

	res, _ := f.do(t, "OPTIONS", "/_meta/actions/test.act", corsAllowedOrigin, map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "Content-Type",
	}, "")
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != corsAllowedOrigin {
		t.Fatalf("action preflight: %d", res.StatusCode)
	}
	if f.mutations.Load() != 0 {
		t.Fatal("preflight executed the action")
	}

	res, body := f.do(t, "POST", "/_meta/actions/test.act", corsAllowedOrigin, map[string]string{
		"Content-Type": "application/json",
	}, `{"name":"Ada"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin action POST status = %d, want 403", res.StatusCode)
	}
	if want := `{"ok":false,"error":"cross-origin action request rejected"}`; body != want {
		t.Fatalf("cross-origin action body = %s, want %s", body, want)
	}
	if f.mutations.Load() != 0 {
		t.Fatal("rejected cross-origin action executed")
	}

	// Same-origin (trusted) actions keep working with CORS active.
	res, body = f.do(t, "POST", "/_meta/actions/test.act", corsForeignTrusted, map[string]string{
		"Content-Type": "application/json",
	}, `{"name":"Ada"}`)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(body, `{"ok":true`) {
		t.Fatalf("trusted action: %d %s", res.StatusCode, body)
	}
	if f.mutations.Load() != 1 {
		t.Fatalf("trusted action executions = %d", f.mutations.Load())
	}
}

// TestCORSFormProtectionIntact keeps the native-form gates intact under CORS:
// a cross-origin form (allowed by CORS but not trusted) is rejected by the
// trusted-origin check before CSRF even runs, a same-origin form still needs a
// valid CSRF token, and the action body limit cannot be bypassed.
func TestCORSFormProtectionIntact(t *testing.T) {
	f := newCORSServer(t, corsTrustedOrigin, corsMiddleware())

	res, body := f.do(t, "POST", "/_meta/actions/test.form", corsBlockedOrigin, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}, "name=Ada&_csrf=bogus")
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Requests from other sites are not accepted.") {
		t.Fatalf("cross-origin form: %d %s", res.StatusCode, body)
	}
	if f.mutations.Load() != 0 {
		t.Fatal("cross-origin form action executed")
	}

	res, _ = f.do(t, "POST", "/_meta/actions/test.act", corsTrustedOrigin, map[string]string{
		"Content-Type": "application/json",
	}, `{"name":"`+strings.Repeat("x", 128)+`"}`)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized action body: %d, want 413", res.StatusCode)
	}
	if f.mutations.Load() != 0 {
		t.Fatal("oversized action executed")
	}

	// Same-origin form with a missing token is still refused by CSRF.
	res, body = f.do(t, "POST", "/_meta/actions/test.form", corsTrustedOrigin, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}, "name=Ada")
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Missing CSRF token") {
		t.Fatalf("missing-token form: %d %s", res.StatusCode, body)
	}
}

// TestCORSRunsAfterApplicationMiddleware pins ordering: application middleware
// registered before the CORS handler runs for every request including
// preflights; anything registered after it never sees a preflight because the
// CORS middleware answers those itself. The documented pipeline is therefore:
// Meta's recovery/observability → application middleware → CORS → routes.
func TestCORSRunsAfterApplicationMiddleware(t *testing.T) {
	var order []string
	mark := func(name string) fiber.Handler {
		return func(c fiber.Ctx) error {
			order = append(order, name)
			return c.Next()
		}
	}
	f := newCORSServer(t, corsTrustedOrigin, mark("app1"), corsMiddleware(), mark("app2"))

	res, _ := f.do(t, "OPTIONS", "/api/posts", corsAllowedOrigin, map[string]string{
		"Access-Control-Request-Method": "GET",
	}, "")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d", res.StatusCode)
	}
	if strings.Join(order, ",") != "app1" {
		t.Fatalf("preflight middleware order = %v, want app1 only (CORS answers before app2)", order)
	}

	order = nil
	res, _ = f.do(t, "GET", "/api/posts", corsAllowedOrigin, nil, "")
	if res.StatusCode != 200 || strings.Join(order, ",") != "app1,app2" {
		t.Fatalf("actual request: %d order=%v", res.StatusCode, order)
	}
}
