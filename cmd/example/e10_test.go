package main

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akashKarmakar02/meta/internal/httpserver"
)

// e10ClientDir stages a dist/client fixture in a temp dir with the real
// manifest shape but fixed names, mirroring production wiring.
func e10ClientDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".vite/manifest.json", `{"frontend/entry.ts": {"file": "assets/entry-E10.js", "isEntry": true}}`)
	write("assets/entry-E10.js", "// entry")
	return dir
}

// TestShellBootsCorrectComponentOnReload (E10 acceptance): a reload of
// each CSR route must deliver a shell whose mount point, route metadata,
// and manifest-referenced entry script are exactly what boots the right
// component — the route ID on the mount point is what selects Settings
// vs Dashboard at runtime.
func TestShellBootsCorrectComponentOnReload(t *testing.T) {
	dir := e10ClientDir(t)
	assets, err := httpserver.LoadAssets(dir)
	if err != nil {
		t.Fatalf("LoadAssets: %v", err)
	}
	app := newApp(&assets)

	cases := []struct {
		path    string
		routeID string
	}{
		{"/dashboard", "dashboard/page.svelte"},
		{"/dashboard/settings", "dashboard/settings/page.svelte"},
	}
	for _, tc := range cases {
		resp, err := app.Test(httptest.NewRequest("GET", tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status = %d", tc.path, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		body := string(b)

		// The E10 shell contract: explicit mount point attribute, route
		// metadata, and the manifest entry script — all present in one
		// document so a reload boots the correct component.
		mount := `<div id="app" data-go-page-root data-go-route="` + tc.path + `" data-go-route-id="` + tc.routeID + `"></div>`
		if !strings.Contains(body, mount) {
			t.Fatalf("%s: missing E10 mount point markup:\nwant %q\nbody:\n%s", tc.path, mount, body)
		}
		if !strings.Contains(body, `<script type="module" src="/assets/entry-E10.js"></script>`) {
			t.Fatalf("%s: missing entry script:\n%s", tc.path, body)
		}
	}
}

// TestSSRShellsAreBare (E10 boundary): SSR routes never carry the page
// root or client bootstrap — they are server-rendered documents.
func TestSSRShellsAreBare(t *testing.T) {
	dir := e10ClientDir(t)
	assets, err := httpserver.LoadAssets(dir)
	if err != nil {
		t.Fatalf("LoadAssets: %v", err)
	}
	app := newApp(&assets)
	for _, path := range []string{"/", "/about", "/docs"} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(b), "data-go-page-root") {
			t.Fatalf("%s: SSR page must not carry the CSR mount point", path)
		}
	}
}
