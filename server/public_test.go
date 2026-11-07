package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/akashKarmakar02/meta/routing"
)

func TestPublicFilesAndPageFallback(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"robots.txt": "robots", "docs/guide.txt": "guide", ".secret": "secret", ".well-known/proof.txt": "proof", "assets/private.txt": "reserved"} {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".secret", filepath.Join(root, "hidden-link")); err != nil {
		t.Fatal(err)
	}
	page := func(routing.Route) (templ.Component, error) { return templ.Raw("<h1>Page</h1>"), nil }
	layout := func(_ routing.Route, child templ.Component) (templ.Component, error) { return child, nil }
	app, err := New(Config{
		PublicDir: root,
		Routes:    []routing.Route{{ID: "page.templ", SourceFile: "page.templ", Path: "/", Mode: routing.ModeSSR}},
		SSRPage:   page, BarePage: page, VisualLayout: layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"GET", "/robots.txt", 200, "robots"},
		{"HEAD", "/robots.txt", 200, ""},
		{"GET", "/docs/guide.txt", 200, "guide"},
		{"GET", "/.well-known/proof.txt", 200, "proof"},
		{"GET", "/", 200, "<h1>Page</h1>"},
		{"GET", "/missing.txt", 404, ""},
		{"GET", "/.secret", 404, ""},
		{"GET", "/assets/private.txt", 404, ""},
		{"GET", "/escape.txt", 404, ""},
		{"GET", "/hidden-link", 404, ""},
		{"GET", "/%2e%2e/secret", 400, ""},
		{"POST", "/robots.txt", 404, ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			res, err := app.Test(httptest.NewRequest(tc.method, tc.path, nil))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", res.StatusCode, tc.status, body)
			}
			if tc.body != "" && !strings.Contains(string(body), tc.body) {
				t.Fatalf("body %q missing %q", body, tc.body)
			}
			if tc.method == http.MethodHead && len(body) != 0 {
				t.Fatalf("HEAD returned body %q", body)
			}
			if tc.path == "/robots.txt" && tc.status == 200 && res.Header.Get("Cache-Control") != "no-cache" {
				t.Fatal("public file must be revalidated")
			}
		})
	}
}
