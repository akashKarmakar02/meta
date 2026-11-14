package metaclerk

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func sessionClaims() map[string]any {
	return map[string]any{
		"iss": "https://clerk.example.com", "sub": "user-private", "sid": "session-private",
		"azp": "https://example.com", "exp": time.Now().Add(time.Minute).Unix(),
		"private": "never-log-raw-claims",
	}
}

func recoveryConfig(t *testing.T) Config {
	t.Helper()
	cfg := testConfig()
	cfg.RecoveryURL = "/auth/recover"
	cfg.Retry.MaxRetries = -1
	body := keyResponse(t, "key-1")
	cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, body), nil
	})}
	return cfg
}

func TestAuthenticationReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   string
		source string
		reason AuthenticationReason
		status int
	}{
		{name: "no credentials", source: "none", reason: AuthMissing, status: 401},
		{name: "valid cookie", kind: "valid", source: "cookie", reason: AuthVerified, status: 200},
		{name: "valid bearer", kind: "valid", source: "authorization", reason: AuthVerified, status: 200},
		{name: "expired cookie", kind: "expired", source: "cookie", reason: AuthExpired, status: 401},
		{name: "expired bearer", kind: "expired", source: "authorization", reason: AuthExpired, status: 401},
		{name: "forged token", kind: "forged", source: "cookie", reason: AuthInvalidSignature, status: 401},
		{name: "forged expired token", kind: "forged-expired", source: "cookie", reason: AuthInvalidSignature, status: 401},
		{name: "wrong authorized party", kind: "party", source: "cookie", reason: AuthInvalidClaims, status: 401},
		{name: "wrong issuer", kind: "issuer", source: "cookie", reason: AuthInvalidClaims, status: 401},
		{name: "missing session ID", kind: "session", source: "cookie", reason: AuthInvalidClaims, status: 401},
		{name: "missing expiry", kind: "expiry", source: "cookie", reason: AuthInvalidClaims, status: 401},
		{name: "future token", kind: "future", source: "cookie", reason: AuthInvalidClaims, status: 401},
		{name: "malformed token", kind: "malformed", source: "cookie", reason: AuthMalformed, status: 401},
		{name: "oversized token", kind: "large", source: "cookie", reason: AuthMalformed, status: 401},
		{name: "malformed authorization takes precedence", kind: "header", source: "authorization", reason: AuthMalformed, status: 401},
		{name: "unknown signing key", kind: "unknown", source: "cookie", reason: AuthUnknownKey, status: 401},
		{name: "signing-key outage", kind: "outage", source: "cookie", reason: AuthKeysUnavailable, status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := recoveryConfig(t)
			events := []AuthenticationEvent{}
			type correlationKey struct{}
			cfg.OnAuthentication = func(ctx context.Context, event AuthenticationEvent) {
				if ctx.Value(correlationKey{}) != "request-42" {
					t.Error("lost correlation context")
				}
				events = append(events, event)
			}
			if tc.kind == "outage" {
				cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(503, "secret-upstream-body"), nil
				})}
			} else if tc.kind == "unknown" {
				cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return response(200, keyResponse(t, "other-key")), nil
				})}
			}
			a := newTestIntegration(t, cfg)
			app := fiber.New(fiber.Config{ReadBufferSize: 32768})
			app.Use(func(c fiber.Ctx) error {
				c.SetContext(context.WithValue(c.Context(), correlationKey{}, "request-42"))
				return c.Next()
			}, a.Middleware(), a.RequireUser())
			app.Get("/", func(c fiber.Ctx) error {
				if _, ok := SessionFromContext(c.Context()); !ok {
					t.Error("verified identity missing")
				}
				return c.SendString("ok")
			})
			claims := sessionClaims()
			switch tc.kind {
			case "expired", "forged-expired":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			case "party":
				claims["azp"] = "https://attacker.invalid"
			case "issuer":
				claims["iss"] = "https://attacker.invalid"
			case "session":
				delete(claims, "sid")
			case "expiry":
				delete(claims, "exp")
			case "future":
				claims["nbf"] = time.Now().Add(time.Hour).Unix()
			}
			token := signedToken(t, claims, strings.HasPrefix(tc.kind, "forged"))
			if tc.kind == "malformed" {
				token = "secret-bad-token"
			}
			if tc.kind == "large" {
				token = strings.Repeat("x", 16385)
			}
			req := httptest.NewRequest("GET", "/", nil)
			if tc.source == "authorization" {
				req.Header.Set("Authorization", "Bearer "+token)
			} else if tc.source == "cookie" {
				req.AddCookie(&http.Cookie{Name: "__session", Value: token})
			}
			if tc.kind == "header" {
				req.Header.Set("Authorization", "Basic secret-authorization")
				req.AddCookie(&http.Cookie{Name: "__session", Value: token})
			}
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status || len(events) != 1 || events[0].Reason != tc.reason || events[0].Source != tc.source {
				t.Fatalf("status=%d events=%+v", res.StatusCode, events)
			}
			if events[0].Duration < 0 {
				t.Fatal("negative duration")
			}
			if tc.status == 401 && string(body) != `{"error":"authentication required"}` {
				t.Fatalf("client error changed: %s", body)
			}
			if tc.status == 503 && (string(body) != "Authentication temporarily unavailable" || res.Header.Get("Cache-Control") != "private, no-store") {
				t.Fatal("key outage response changed or cacheable")
			}
			for _, secret := range []string{token, cfg.SecretKey, "user-private", "session-private", "never-log-raw-claims", "secret-upstream-body", "secret-authorization"} {
				if strings.Contains(fmt.Sprint(events, string(body)), secret) {
					t.Fatal("sensitive data in diagnostic or response")
				}
			}
		})
	}
}

func recoveryApp(t *testing.T, cfg Config) *fiber.App {
	t.Helper()
	a := newTestIntegration(t, cfg)
	app := fiber.New()
	app.Use(a.Middleware())
	app.Get("/auth/recover", a.RecoveryPage(), func(c fiber.Ctx) error {
		data, ok := RecoveryFromContext(c.Context())
		if !ok {
			t.Error("recovery data missing")
		}
		if _, authenticated := SessionFromContext(c.Context()); authenticated {
			t.Error("expired token authenticated")
		}
		return c.JSON(data)
	})
	app.All("/auth/session", a.RecoveryStatus())
	app.All("/dashboard", a.RequirePageUser(), func(c fiber.Ctx) error { return c.SendString("protected") })
	return app
}

func TestPageRecoveryAndLoopPrevention(t *testing.T) {
	for _, navigation := range []string{"document", "soft"} {
		t.Run(navigation, func(t *testing.T) {
			app := recoveryApp(t, recoveryConfig(t))
			claims := sessionClaims()
			claims["exp"] = time.Now().Add(-time.Minute).Unix()
			expired := &http.Cookie{Name: "__session", Value: signedToken(t, claims, false)}
			target := "/dashboard?tab=team&filter=a%26b"
			req := httptest.NewRequest("GET", target, nil)
			req.AddCookie(expired)
			if navigation == "soft" {
				req.Header.Set("X-Go-Web-Navigation", "1")
			}
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			location, err := url.Parse(res.Header.Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != 303 || location.Path != "/auth/recover" || location.Query().Get("return_to") != target {
				t.Fatalf("status=%d location=%s", res.StatusCode, location)
			}
			var marker *http.Cookie
			for _, cookie := range res.Cookies() {
				if cookie.Name == recoveryCookie {
					marker = cookie
				}
			}
			if marker == nil || !marker.HttpOnly || marker.MaxAge != 120 {
				t.Fatal("bounded loop marker missing")
			}
			// Neither an expiry diagnostic nor arrival at the recovery page authenticates.
			req = httptest.NewRequest("GET", location.String(), nil)
			req.AddCookie(expired)
			res, err = app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("recovery page status=%d", res.StatusCode)
			}
			// An unchanged/expired cookie cannot automatically resume forever.
			req = httptest.NewRequest("GET", target, nil)
			req.AddCookie(expired)
			req.AddCookie(marker)
			res, err = app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			location, err = url.Parse(res.Header.Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if location.Query().Get("retry") != "1" {
				t.Fatal("failed resume did not require explicit retry")
			}
			// Only a fresh, verified browser cookie passes the probe and protected page.
			fresh := &http.Cookie{Name: "__session", Value: signedToken(t, sessionClaims(), false)}
			for _, endpoint := range []string{"/auth/session", target} {
				req = httptest.NewRequest("GET", endpoint, nil)
				req.AddCookie(fresh)
				req.AddCookie(marker)
				res, err = app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if endpoint == "/auth/session" && res.StatusCode != 204 {
					t.Fatalf("probe status=%d", res.StatusCode)
				}
				if endpoint == target {
					if res.StatusCode != 200 {
						t.Fatalf("protected status=%d", res.StatusCode)
					}
					if len(res.Cookies()) == 0 || res.Cookies()[0].MaxAge != -1 {
						t.Fatal("successful page did not clear loop guard")
					}
				}
			}
		})
	}
}

func TestRecoveryDestinations(t *testing.T) {
	for _, destination := range []string{
		"https://evil.invalid", "//evil.invalid", "/\\evil.invalid", "/%2f/evil.invalid", "/%5cevil.invalid",
		"/%252f%252fevil.invalid", "/%0d%0aLocation:evil", "/\tevil", "/auth/recover", "/auth/recover/",
		"/x/../auth/recover", "/auth/%72ecover", "/sign-in", "/%73ign-in", "", "/%zz",
	} {
		t.Run(destination, func(t *testing.T) {
			app := recoveryApp(t, recoveryConfig(t))
			req := httptest.NewRequest("GET", "/auth/recover?return_to="+url.QueryEscape(destination), nil)
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != 400 || res.Header.Get("Location") != "" {
				t.Fatalf("accepted unsafe destination, status=%d", res.StatusCode)
			}
		})
	}
}

func TestRecoveryConfigurationAndLegacyBehavior(t *testing.T) {
	for _, destination := range []string{"//evil.invalid", "/%2f/evil", "/sign-in", "/recover?x=1", "/recover#x"} {
		t.Run(destination, func(t *testing.T) {
			cfg := testConfig()
			cfg.RecoveryURL = destination
			if _, err := New(cfg); err == nil {
				t.Fatal("accepted invalid recovery configuration")
			}
		})
	}
	app := recoveryApp(t, testConfig())
	res, err := app.Test(httptest.NewRequest("GET", "/dashboard", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != "/sign-in" {
		t.Fatal("legacy redirect changed")
	}
}

func TestRecoveryNeverUsesBearerAsCookieProof(t *testing.T) {
	app := recoveryApp(t, recoveryConfig(t))
	req := httptest.NewRequest("GET", "/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken(t, sessionClaims(), false))
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("bearer authenticated cookie probe")
	}
	res, err = app.Test(httptest.NewRequest("POST", "/dashboard", nil))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 || res.Header.Get("Location") != "" {
		t.Fatal("mutation redirected")
	}
}
