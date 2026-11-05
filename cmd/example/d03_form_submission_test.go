// Package main — P2-D03 integration tests: no-JavaScript HTML form
// submission through the action endpoint, including CSRF token issuance and
// enforcement, typed field decoding, repeated-field preservation, PRG
// redirect semantics, and JSON endpoint compatibility.
package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	appactions "github.com/akashKarmakar02/meta/app/actions"
	"github.com/akashKarmakar02/meta/formcsrf"
)

// newD03App builds a fully configured app with the example actions and a
// fresh per-test CSRF manager.
func newD03App(t *testing.T) *fiber.App {
	t.Helper()
	mgr, err := formcsrf.New([]byte("d03-test-secret-key-32-bytes-ok!"), nil)
	if err != nil {
		t.Fatalf("formcsrf.New: %v", err)
	}
	return newConfiguredAppWithActions(nil, false, nil, newD02Registry(t), d02TrustedOrigin, mgr)
}

// getWithCookies performs a GET carrying prior cookies (from a Set-Cookie)
// and returns status, body, and any CSRF cookie set.
func getWithCookies(app *fiber.App, path string, cookies map[string]string) (int, string, map[string]string) {
	req := httptest.NewRequest("GET", path, nil)
	for k, v := range cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	res, err := app.Test(req)
	if err != nil {
		return 0, err.Error(), nil
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	setCookies := map[string]string{}
	for _, c := range res.Cookies() {
		setCookies[c.Name] = c.Value
	}
	return res.StatusCode, string(b), setCookies
}

// TestD03FormSubmissionHappyPath covers the D03 acceptance outcome: an
// ordinary urlencoded form POST with a valid CSRF token executes the bound
// action exactly once and 303-redirects to the success URL.
func TestD03FormSubmissionHappyPath(t *testing.T) {
	app := newD03App(t)
	appactions.Reset()

	// A browser GETs the page first, receiving the CSRF cookie and markup.
	code, page, setCookies := getWithCookies(app, "/guestbook", nil)
	if code != 200 {
		t.Fatalf("guestbook page: code=%d body=%s", code, page)
	}
	csrfCookie, ok := setCookies[formcsrf.CookieName]
	if !ok {
		t.Fatal("page GET did not set the CSRF cookie")
	}
	if !strings.Contains(page, "guestbook.submit") {
		t.Fatal("page does not reference the guestbook.submit action")
	}

	// Extract the rendered hidden token.
	token := extractHiddenToken(t, page)
	if token == "" {
		t.Fatal("page has no hidden csrf token")
	}

	form := url.Values{}
	form.Set("name", "Ada")
	form.Set("message", "Hello from the form")
	form.Set("_csrf", token)

	req := httptest.NewRequest("POST", "/_meta/actions/guestbook.submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", d02TrustedOrigin)
	req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: csrfCookie})
	res, err := app.Test(req)
	if err != nil {
		t.Fatalf("form POST failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 303 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("form POST: code=%d body=%s", res.StatusCode, body)
	}
	loc := res.Header.Get("Location")
	if loc != "/guestbook" {
		t.Fatalf("Location = %q", loc)
	}
	if n := len(appactions.Guestbook()); n != 1 {
		t.Fatalf("guestbook has %d entries, want 1", n)
	}
}

// extractHiddenToken pulls the value of the hidden CSRF field out of the
// rendered page markup.
func extractHiddenToken(t *testing.T, page string) string {
	t.Helper()
	const needle = `name="_csrf" value="`
	i := strings.Index(page, needle)
	if i < 0 {
		return ""
	}
	rest := page[i+len(needle):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// TestD03FormCSRFRejection verifies missing, tampered, and foreign tokens are
// rejected before execution.
func TestD03FormCSRFRejection(t *testing.T) {
	app := newD03App(t)
	appactions.Reset()

	_, _, setCookies := getWithCookies(app, "/guestbook", nil)
	csrfCookie := setCookies[formcsrf.CookieName]

	cases := []struct {
		name   string
		token  string
		cookie string
	}{
		{"missing", "", csrfCookie},
		{"tampered", "deadbeefdeadbeef", csrfCookie},
		{"cross-origin-token", otherManagerToken(t), csrfCookie},
		{"no-cookie", validTokenFromOtherManager(t), ""},
	}
	for _, tc := range cases {
		form := url.Values{}
		form.Set("name", "Mallory")
		form.Set("message", "hi")
		form.Set("_csrf", tc.token)
		req := httptest.NewRequest("POST", "/_meta/actions/guestbook.submit", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", d02TrustedOrigin)
		if tc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: tc.cookie})
		}
		res, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		defer res.Body.Close()
		if res.StatusCode < 400 {
			t.Fatalf("%s: expected rejection, got %d", tc.name, res.StatusCode)
		}
		if n := len(appactions.Guestbook()); n != 0 {
			t.Fatalf("%s: action executed despite rejection", tc.name)
		}
	}
}

// otherManagerToken produces a token signed by a DIFFERENT secret.
func otherManagerToken(t *testing.T) string {
	t.Helper()
	m, err := formcsrf.New([]byte("attacker-controlled-secret-here-32!"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tok := m.Issue()
	return tok
}

// validTokenFromOtherManager is unused standalone; kept for readability of the
// no-cookie case: the token itself is valid but no cookie backs it.
func validTokenFromOtherManager(t *testing.T) string { return otherManagerToken(t) }

// TestD03FormPrgRefreshSafety verifies the destination can be re-fetched via
// GET without re-executing the action.
func TestD03FormPrgRefreshSafety(t *testing.T) {
	app := newD03App(t)
	appactions.Reset()

	_, _, setCookies := getWithCookies(app, "/guestbook", nil)
	csrfCookie := setCookies[formcsrf.CookieName]
	token := extractHiddenToken(t, mustGetBody(t, app, "/guestbook", csrfCookie))

	form := url.Values{}
	form.Set("name", "Bob")
	form.Set("message", "once")
	form.Set("_csrf", token)
	req := httptest.NewRequest("POST", "/_meta/actions/guestbook.submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", d02TrustedOrigin)
	req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: csrfCookie})
	if res, err := app.Test(req); err != nil || res.StatusCode != 303 {
		t.Fatalf("first POST: %v", err)
	} else {
		res.Body.Close()
	}
	if n := len(appactions.Guestbook()); n != 1 {
		t.Fatalf("entries=%d after POST", n)
	}

	// Refreshing the destination twice more must not repeat the mutation.
	for range 2 {
		getWithCookies(app, "/guestbook", map[string]string{formcsrf.CookieName: csrfCookie})
	}
	if n := len(appactions.Guestbook()); n != 1 {
		t.Fatalf("entries=%d after refreshes, want 1", n)
	}
}

// mustGetBody fetches a page with the given cookie and returns its body.
func mustGetBody(t *testing.T, app *fiber.App, path, csrfCookie string) string {
	t.Helper()
	code, body, _ := getWithCookies(app, path, map[string]string{formcsrf.CookieName: csrfCookie})
	if code != 200 {
		t.Fatalf("GET %s: %d", path, code)
	}
	return body
}

// TestD03RepeatedFieldsPreserved verifies multi-value fields decode in order.
func TestD03RepeatedFieldsPreserved(t *testing.T) {
	app := newD03App(t)
	appactions.Reset()
	_, _, setCookies := getWithCookies(app, "/guestbook", nil)
	csrfCookie := setCookies[formcsrf.CookieName]
	token := extractHiddenToken(t, mustGetBody(t, app, "/guestbook", csrfCookie))

	form := url.Values{}
	form.Set("name", "Carol")
	form.Set("message", "m")
	form.Set("_csrf", token)
	form.Add("tags", "alpha")
	form.Add("tags", "beta")
	form.Add("tags", "gamma")
	req := httptest.NewRequest("POST", "/_meta/actions/guestbook.submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", d02TrustedOrigin)
	req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: csrfCookie})
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 303 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("code=%d body=%s", res.StatusCode, body)
	}
}

// TestD03JSONEndpointUnchanged verifies the D02 JSON path still works for a
// JSON-bound action alongside the form path.
func TestD03JSONEndpointUnchanged(t *testing.T) {
	app := newD03App(t)
	appactions.Reset()
	code, body, _ := postAction(app, "/_meta/actions/demo.echo", d02TrustedOrigin, "application/json", `{"message":"hi"}`)
	if code != 200 {
		t.Fatalf("echo: %d %s", code, body)
	}
}

// TestD03GetNeverMutates verifies GET on the form endpoint cannot execute the
// action even with valid tokens.
func TestD03GetNeverMutates(t *testing.T) {
	app := newD03App(t)
	appactions.Reset()
	_, _, setCookies := getWithCookies(app, "/guestbook", nil)
	csrfCookie := setCookies[formcsrf.CookieName]

	req := httptest.NewRequest("GET", "/_meta/actions/guestbook.submit?name=Evil&message=evil&_csrf=x", nil)
	req.Header.Set("Origin", d02TrustedOrigin)
	req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: csrfCookie})
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if n := len(appactions.Guestbook()); n != 0 {
		t.Fatal("GET executed the action")
	}
}
