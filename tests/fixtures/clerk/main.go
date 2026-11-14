// Browser-only fixture: real Meta routing and Clerk JWT verification, with
// synthetic signing keys and a simulated ClerkJS/FAPI boundary. Never deploy it.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	metaclerk "github.com/akashKarmakar02/meta/integrations/clerk"
	"github.com/akashKarmakar02/meta/loader"
	"github.com/akashKarmakar02/meta/routecontext"
	"github.com/akashKarmakar02/meta/routing"
	"github.com/akashKarmakar02/meta/server"
	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

var privateKey = ed25519.NewKeyFromSeed([]byte(strings.Repeat("f", ed25519.SeedSize)))

func token(kind string) (string, error) {
	key := privateKey
	if kind == "forged" {
		key = ed25519.NewKeyFromSeed([]byte(strings.Repeat("x", ed25519.SeedSize)))
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "fixture"))
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(time.Minute)
	if kind == "expired" {
		exp = time.Now().Add(-time.Minute)
	}
	return josejwt.Signed(signer).Claims(map[string]any{
		"iss": "https://clerk.fixture.invalid", "sub": "fixture-user", "sid": "fixture-session",
		"azp": "http://localhost:3135", "exp": exp.Unix(),
	}).Serialize()
}

func main() {
	keys, err := json.Marshal(map[string]any{"keys": []jose.JSONWebKey{{Key: privateKey.Public(), KeyID: "fixture", Algorithm: "EdDSA", Use: "sig"}}})
	if err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	events := []metaclerk.AuthenticationEvent{}
	config := metaclerk.Config{
		SecretKey: "sk_test_fixture_only", SignInURL: "/sign-in", RecoveryURL: "/auth/recover",
		AuthorizedParties: []string{"http://localhost:3135"}, Retry: metaclerk.RetryConfig{MaxRetries: -1},
		HTTPClient: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(keys)))}, nil
		})},
		OnAuthentication: func(_ context.Context, event metaclerk.AuthenticationEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		},
	}
	auth, err := metaclerk.New(config)
	if err != nil {
		log.Fatal(err)
	}
	config.HTTPClient = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})}
	outage, err := metaclerk.New(config)
	if err != nil {
		log.Fatal(err)
	}
	routes := []routing.Route{}
	for _, spec := range []struct {
		path, file string
		mode       routing.RenderMode
	}{
		{path: "/", file: "page.templ", mode: routing.ModeSSR},
		{path: "/dashboard", file: "dashboard/page.templ", mode: routing.ModeSSR},
		{path: "/sign-in", file: "sign-in/page.templ", mode: routing.ModeSSR},
		{path: "/auth/recover", file: "auth/recover/page.svelte", mode: routing.ModeCSR},
	} {
		route, err := routing.NewRoute(spec.path, spec.file, spec.mode, nil)
		if err != nil {
			log.Fatal(err)
		}
		routes = append(routes, route)
	}
	page := func(route routing.Route) (templ.Component, error) {
		return templ.ComponentFunc(func(ctx context.Context, out io.Writer) error {
			content := `<main><h1>Landing</h1><a href="/dashboard?tab=team">Dashboard</a></main>`
			if route.Path == "/dashboard" {
				if _, ok := metaclerk.SessionFromContext(ctx); !ok {
					return errors.New("fixture render without verified session")
				}
				content = `<main><h1>Dashboard</h1><p>Protected SSR content</p></main>`
			} else if route.Path == "/sign-in" {
				content = `<main><h1>Interactive sign-in</h1></main>`
			}
			_, err := io.WriteString(out, content)
			return err
		}), nil
	}
	app, err := server.New(server.Config{
		Routes: routes, ClientDir: "dist/clerk-fixture", SSRPage: page, BarePage: page,
		VisualLayout: func(_ routing.Route, child templ.Component) (templ.Component, error) { return child, nil },
		Loaders: []loader.Binding{loader.Bind("auth/recover/page.svelte", func(ctx context.Context, _ routecontext.Route) (metaclerk.Recovery, error) {
			data, ok := metaclerk.RecoveryFromContext(ctx)
			if !ok {
				return data, loader.ErrInvalidInput
			}
			return data, nil
		})},
		Middleware: []fiber.Handler{
			func(c fiber.Ctx) error {
				if c.Path() == "/fixture/token" {
					value, err := token(c.Query("kind"))
					if err != nil {
						return err
					}
					c.Cookie(&fiber.Cookie{Name: "__session", Value: value, Path: "/", SameSite: "Lax"})
					return c.SendStatus(204)
				}
				if c.Path() == "/fixture/refresh" {
					if c.Cookies("fixture_mode") == "refresh-failure" {
						return c.SendStatus(503)
					}
					if c.Cookies("fixture_mode") == "stale-refresh" {
						return c.SendStatus(204)
					}
					value, err := token("valid")
					if err != nil {
						return err
					}
					c.Cookie(&fiber.Cookie{Name: "__session", Value: value, Path: "/", SameSite: "Lax"})
					return c.SendStatus(204)
				}
				if c.Path() == "/fixture/client" {
					return c.JSON(fiber.Map{"signedIn": c.Cookies("fixture_mode") != "signed-out"})
				}
				if c.Path() == "/fixture/diagnostics" {
					mu.Lock()
					defer mu.Unlock()
					return c.JSON(events)
				}
				if c.Query("outage") == "1" {
					return outage.Middleware()(c)
				}
				return auth.Middleware()(c)
			},
			func(c fiber.Ctx) error {
				switch c.Path() {
				case "/auth/recover":
					return auth.RecoveryPage()(c)
				case "/auth/session":
					return auth.RecoveryStatus()(c)
				}
				return c.Next()
			},
		},
		Policies: []server.Policy{server.Protect("/dashboard/*", auth.RequirePageUser())},
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(app.Listen(":" + os.Getenv("PORT")))
}
