package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	appactions "go-meta/app/actions"
	"go-meta/formcsrf"
)

func TestD05EnhancedFormNegotiationAndCSRF(t *testing.T) {
	app := newD03App(t)
	appactions.Reset()
	_, _, cookies := getWithCookies(app, "/guestbook", nil)
	cookie := cookies[formcsrf.CookieName]
	token := extractHiddenToken(t, mustGetBody(t, app, "/guestbook", cookie))
	submit := func(name, csrf, origin string, enhanced bool) (*http.Response, string) {
		values := url.Values{"name": {name}, "message": {"hello"}, "_csrf": {csrf}}
		req := httptest.NewRequest("POST", "/_meta/actions/guestbook.submit", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", origin)
		if enhanced {
			req.Header.Set("X-Go-Meta-Action", "1")
			req.Header.Set("Accept", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: cookie})
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(data)
	}
	resp, body := submit(" ", token, d02TrustedOrigin, true)
	var result map[string]any
	if err := json.Unmarshal([]byte(body), &result); err != nil || resp.StatusCode != 422 || result["error"] != "validation failed" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("validation: %d %s", resp.StatusCode, body)
	}
	resp, body = submit("Alice", "tampered", d02TrustedOrigin, true)
	if resp.StatusCode != 403 || !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("csrf: %d %s", resp.StatusCode, body)
	}
	resp, _ = submit("Alice", token, "https://attacker.example", true)
	if resp.StatusCode != 403 {
		t.Fatalf("origin: %d", resp.StatusCode)
	}
	resp, body = submit("Alice", token, d02TrustedOrigin, true)
	if resp.StatusCode != 200 || body != `{"ok":true,"redirect":"/guestbook"}` || appactions.Calls.Guestbook.Load() != 2 {
		t.Fatalf("success: %d %s calls=%d", resp.StatusCode, body, appactions.Calls.Guestbook.Load())
	}
	resp, _ = submit("Bob", token, d02TrustedOrigin, false)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/guestbook" {
		t.Fatalf("native: %d", resp.StatusCode)
	}
}
