package main

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

	"github.com/akashKarmakar02/meta/action"
	appactions "github.com/akashKarmakar02/meta/app/actions"
	"github.com/akashKarmakar02/meta/app/guestbookform"
	"github.com/akashKarmakar02/meta/formcsrf"
	"github.com/akashKarmakar02/meta/routecontext"
)

func TestD04NativeFeedbackAndResubmit(t *testing.T) {
	appactions.Reset()
	app := newD03App(t)
	_, _, cookies := getWithCookies(app, "/guestbook", nil)
	cookie := cookies[formcsrf.CookieName]
	token := extractHiddenToken(t, mustGetBody(t, app, "/guestbook", cookie))
	submit := func(values url.Values) *http.Response {
		values.Set("_csrf", token)
		req := httptest.NewRequest("POST", "/_meta/actions/guestbook.submit", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://localhost:3000")
		req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: cookie})
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := submit(url.Values{"name": {" "}, "message": {"<script>alert(1)</script>"}, "password": {"secret"}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 422 || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Location") != "" {
		t.Fatalf("invalid response: %d %v", resp.StatusCode, resp.Header)
	}
	html := string(body)
	for _, want := range []string{"Name is required", `aria-invalid="true"`, `aria-describedby="guestbook-name-error"`, "&lt;script&gt;", token} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %s", want, html)
		}
	}
	if strings.Contains(html, "secret") || strings.Contains(html, "<script>") {
		t.Fatal("unsafe reflected input")
	}
	resp = submit(url.Values{"name": {"Alice"}, "message": {"Hello"}})
	resp.Body.Close()
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/guestbook" || len(appactions.Guestbook()) != 1 || appactions.Calls.Guestbook.Load() != 2 {
		t.Fatalf("resubmit: status %d calls %d", resp.StatusCode, appactions.Calls.Guestbook.Load())
	}
}

func TestD04JSONValidation(t *testing.T) {
	app := newD03App(t)
	code, body, contentType := postAction(app, "/_meta/actions/guestbook.submit", d02TrustedOrigin, "application/json", `{"Name":"","Message":""}`)
	if code != 422 || !strings.HasPrefix(contentType, "application/json") || !strings.Contains(body, `"name":["Name is required"]`) || !strings.Contains(body, `"message":["Message is required"]`) {
		t.Fatalf("unexpected response %d %s %s", code, contentType, body)
	}
}

func TestD04DecoderValidationDoesNotExecute(t *testing.T) {
	var calls atomic.Int32
	act := func(_ context.Context, _ routecontext.Route, in string) (string, error) { calls.Add(1); return in, nil }
	decoder := func(values url.Values) (string, error) {
		return "", action.NewValidationError("Correct the form", map[string][]string{"name": {"Invalid number"}})
	}
	binding := action.BindForm("test.decode", act, decoder, "/guestbook").WithFormFeedback([]string{"name"}, guestbookform.Feedback)
	reg, err := action.NewRegistry(binding)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := formcsrf.New([]byte("d03-test-secret-key-32-bytes-ok!"), nil)
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredAppWithActions(nil, false, nil, reg, d02TrustedOrigin, manager)
	token := manager.Issue()
	req := httptest.NewRequest("POST", "/_meta/actions/test.decode", strings.NewReader(url.Values{"_csrf": {token}, "name": {"abc"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", d02TrustedOrigin)
	req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: token})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 422 || calls.Load() != 0 || !strings.Contains(string(body), "Invalid number") || !strings.Contains(string(body), `value="abc"`) {
		t.Fatalf("decoder validation response: %d %s calls=%d", resp.StatusCode, body, calls.Load())
	}
}

func TestD04HTTPSCookie(t *testing.T) {
	manager, err := formcsrf.New([]byte("d03-test-secret-key-32-bytes-ok!"), nil)
	if err != nil {
		t.Fatal(err)
	}
	app := newConfiguredAppWithActions(nil, false, nil, newD02Registry(t), "https://example.com", manager)
	req := httptest.NewRequest("GET", "https://example.com/guestbook", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, cookie := range resp.Cookies() {
		if cookie.Name == formcsrf.SecureCookieName {
			if !cookie.Secure || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("bad cookie: %+v", cookie)
			}
			if resp.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("token page must not be cached")
			}
			return
		}
	}
	t.Fatal("secure host cookie missing")
}

func TestD04ConcurrentFeedbackIsolation(t *testing.T) {
	app := newD03App(t)
	manager, err := formcsrf.New([]byte("d03-test-secret-key-32-bytes-ok!"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			token := manager.Issue()
			marker := fmt.Sprintf("request-%d", i)
			body := url.Values{"_csrf": {token}, "name": {" "}, "message": {marker}}.Encode()
			req := httptest.NewRequest("POST", "/_meta/actions/guestbook.submit", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", d02TrustedOrigin)
			req.AddCookie(&http.Cookie{Name: formcsrf.CookieName, Value: token})
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			html, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 422 || !strings.Contains(string(html), marker) || !strings.Contains(string(html), token) {
				t.Fatalf("wrong response: %d %s", resp.StatusCode, html)
			}
		})
	}
}
