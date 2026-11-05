package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImageEndpointInApplication(t *testing.T) {
	app := newConfiguredApp(nil, false, nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/_meta/image?src=%2Fimages%2Fhero.jpg&w=960", 501},
		{"HEAD", "/_meta/image?src=%2Fimages%2Fhero.jpg&w=960", 501},
		{"GET", "/_meta/image?src=%2Fetc%2Fpasswd&w=1", 400},
		{"POST", "/_meta/image", 405},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Go-Web-Navigation", "1")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || strings.Contains(string(body), "passwd") {
			t.Fatalf("%s %s: %d %q", tc.method, tc.path, resp.StatusCode, body)
		}
	}
}
